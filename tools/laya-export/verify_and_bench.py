"""Verify the exported ONNX model against the PyTorch reference and benchmark CPU latency.

Usage: python verify_and_bench.py <model_dir> <onnx_dir>
"""
import json
import os
import sys
import time

import numpy as np
import torch

MODEL_DIR = sys.argv[1]
ONNX_DIR = sys.argv[2]

from laya.agent import Agent
from laya.common import collate_items
import onnxruntime as ort

STATE = {"body": "Функция ValidateToken проверяет токен сессии и возвращает userID"}
QUESTIONS = {
    "noul": {
        "type": "noul",
        "instructions": "Достаточно ли этого контекста чтобы ответить где проверяется токен?",
    },
    "relevance": {
        "type": "score",
        "instructions": "Насколько этот чанк релевантен запросу 'где проверяется токен'?",
        "criteria": [
            "совсем не релевантен",
            "почти не релевантен",
            "частично релевантен",
            "в основном релевантен",
            "сильно релевантен",
            "точно отвечает на запрос",
        ],
    },
}

# --- PyTorch reference -------------------------------------------------------
agent = Agent(MODEL_DIR, device="cpu")
ids = list(QUESTIONS.keys())
internal = {qid: Agent._to_internal(QUESTIONS[qid]) for qid in ids}
items = agent._encode_state(STATE, ids, internal)
b = collate_items([items], agent.tok.pad_token_id)

print("=== tokenized rows ===")
for it in items:
    print("qtype=%d len=%d markers=%s" % (it["qtype"], len(it["ids"]), it["markers"]))
    print("text:", agent.tok.decode(it["ids"]))

with torch.no_grad():
    ref_logits, ref_act = agent.model(
        b["input_ids"], b["attention_mask"], b["marker_pos"], b["marker_mask"], b["qtype"])
ref_logits = ref_logits.numpy()
ref_act = torch.softmax(ref_act.float(), -1).numpy()

# --- ONNX runtime ------------------------------------------------------------
so = ort.SessionOptions()
so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
sess = ort.InferenceSession(os.path.join(ONNX_DIR, "laya.onnx"), sess_options=so,
                            providers=["CPUExecutionProvider"])
ort_inputs = {
    "input_ids": b["input_ids"].numpy().astype(np.int64),
    "attention_mask": b["attention_mask"].numpy().astype(np.int64),
    "marker_pos": b["marker_pos"].numpy().astype(np.int64),
    "marker_mask": b["marker_mask"].numpy().astype(bool),
    "qtype": b["qtype"].numpy().astype(np.int64),
}
onx_logits, onx_act_logits = sess.run(["logits", "act_logits"], ort_inputs)
onx_act = np.exp(onx_act_logits - onx_act_logits.max(-1, keepdims=True))
onx_act = onx_act / onx_act.sum(-1, keepdims=True)

print("\n=== parity (PyTorch fp32 vs ONNX fp32) ===")
d = np.abs(onx_logits - ref_logits)
valid = ref_logits > -1e3  # ignore -1e4 masked slots
print("max |dlogits| (all slots)  = %.3e" % d.max())
print("max |dlogits| (valid slots)= %.3e" % d[valid].max())
print("max |dact_probs|           = %.3e" % np.abs(onx_act - ref_act).max())
for r, qid in enumerate(ids):
    k = len(items[r]["markers"])
    print("row %d (%s): torch=%s onnx=%s" % (
        r, qid, np.array2string(ref_logits[r, :k], precision=5),
        np.array2string(onx_logits[r, :k], precision=5)))

# --- end-to-end answers, both runtimes --------------------------------------
from laya.onnx_agent import ONNXAgent
onx_agent = ONNXAgent(MODEL_DIR, onnx_path=os.path.join(ONNX_DIR, "laya.onnx"))
ref_ans = agent.system_one(STATE, QUESTIONS)
onx_ans = onx_agent.system_one(STATE, QUESTIONS)
print("\n=== answers (torch) ===")
print(json.dumps(ref_ans["answers"], ensure_ascii=False, indent=1))
print("=== answers (onnx) ===")
print(json.dumps(onx_ans["answers"], ensure_ascii=False, indent=1))

# --- latency ------------------------------------------------------------------
def make_row(n_tokens, n_opts=2, qtype=2):
    ids_row = np.random.randint(5, 256000, size=(1, n_tokens)).astype(np.int64)
    ids_row[0, 0] = agent.tok.cls_token_id
    ids_row[0, -1] = agent.tok.sep_token_id
    return {
        "input_ids": ids_row,
        "attention_mask": np.ones((1, n_tokens), dtype=np.int64),
        "marker_pos": np.arange(4, 4 + n_opts, dtype=np.int64)[None, :],
        "marker_mask": np.ones((1, n_opts), dtype=bool),
        "qtype": np.array([qtype], dtype=np.int64),
    }

print("\n=== ONNX CPU latency (batch=1) ===")
print("cpu threads: ORT default (%d physical cores)" % (os.cpu_count() or 0))
for n in (250, 500):
    feed = make_row(n)
    for _ in range(3):
        sess.run(None, feed)  # warmup
    ts = []
    for _ in range(15):
        t0 = time.perf_counter()
        sess.run(None, feed)
        ts.append((time.perf_counter() - t0) * 1e3)
    ts.sort()
    print("L=%3d tokens: median %.1f ms  min %.1f ms  max %.1f ms" % (n, ts[len(ts)//2], ts[0], ts[-1]))

# realistic call: one state, two questions, through the full pipeline
for _ in range(2):
    onx_agent.system_one(STATE, QUESTIONS)
ts = []
for _ in range(10):
    t0 = time.perf_counter()
    onx_agent.system_one(STATE, QUESTIONS)
    ts.append((time.perf_counter() - t0) * 1e3)
ts.sort()
L = max(len(it["ids"]) for it in items)
print("end-to-end system_one (2 questions, L=%d): median %.1f ms  min %.1f ms" % (L, ts[len(ts)//2], ts[0]))
