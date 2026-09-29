"""Динамическая int8-квантизация Laya ONNX (fp32 -> int8 по Linear/MatMul).

Usage: python quantize_int8.py <model_dir>
Читает <model_dir>/laya.onnx (+ .data), пишет <model_dir>/laya-int8.onnx (+ .data)
и сравнивает выходы fp32 vs int8 на тестовом кейсе (max |Δlogits|).

Офлайн-инструмент (не часть продукта). Критерий приёмки из ТЗ:
квантизация деплоится только если метрики не просели — сначала смотрим
расхождение логитов, затем прогон eval с int8-моделью.
"""

import os
import sys
import time

import numpy as np
import onnx
import onnxruntime as ort
from onnxruntime.quantization import QuantType, quantize_dynamic

MODEL_DIR = sys.argv[1] if len(sys.argv) > 1 and not sys.argv[1].startswith("--") else "models/laya-multilingual"
_extra = [a for a in sys.argv[2:] if not a.startswith("--")]
HF_MODEL = _extra[0] if _extra else "convaiinnovations/laya-multilingual"
SRC = os.path.join(MODEL_DIR, "laya.onnx")
TMP = os.path.join(MODEL_DIR, "laya-stripped.onnx")
DST = os.path.join(MODEL_DIR, "laya-int8.onnx")


def strip_value_info():
    """Экспорт оставил устаревшие shape-аннотации (конфликт 772 vs 256 в
    act-head) — shape inference квантизатора падает. Стираем value_info:
    инференс пересчитает формы заново. Веса остаются во внешнем .data."""
    print("stripping stale value_info ...")
    m = onnx.load(SRC)
    del m.graph.value_info[:]
    onnx.save_model(m, TMP, save_as_external_data=True,
                    all_tensors_to_one_file=True,
                    location="laya-stripped.onnx.data",
                    size_threshold=1024)


def main():
    if "--parity-only" not in sys.argv:
        strip_value_info()
        print(f"quantizing {TMP} -> {DST} ...")
        t0 = time.time()
        quantize_dynamic(
            TMP,
            DST,
            weight_type=QuantType.QInt8,
            op_types_to_quantize=["MatMul", "Gemm"],
        )
        print(f"done in {time.time()-t0:.0f}s")

        src_size = os.path.getsize(SRC) + os.path.getsize(SRC + ".data")
        dst_size = os.path.getsize(DST) + (os.path.getsize(DST + ".data") if os.path.exists(DST + ".data") else 0)
        print(f"size: {src_size/2**20:.0f} MB -> {dst_size/2**20:.0f} MB")

    # --- parity check на русском кейсе (та же сборка входов, что в verify_and_bench.py) ---
    from laya.agent import Agent
    from laya.common import collate_items

    STATE = {"body": "Функция ValidateToken проверяет токен сессии и возвращает userID"}
    QUESTIONS = {
        "noul": {"type": "noul",
                 "instructions": "Достаточно ли этого контекста чтобы ответить где проверяется токен?"},
        "relevance": {"type": "score",
                      "instructions": "Насколько этот чанк релевантен запросу 'где проверяется токен'?",
                      "criteria": ["совсем не релевантен", "почти не релевантен", "частично релевантен",
                                   "в основном релевантен", "сильно релевантен", "точно отвечает на запрос"]},
    }
    agent = Agent(HF_MODEL, device="cpu")
    ids = list(QUESTIONS.keys())
    internal = {qid: Agent._to_internal(QUESTIONS[qid]) for qid in ids}
    items = agent._encode_state(STATE, ids, internal)
    b = collate_items([items], agent.tok.pad_token_id)
    inputs = {
        "input_ids": b["input_ids"].numpy().astype(np.int64),
        "attention_mask": b["attention_mask"].numpy().astype(np.int64),
        "marker_pos": b["marker_pos"].numpy().astype(np.int64),
        "marker_mask": b["marker_mask"].numpy().astype(bool),
        "qtype": b["qtype"].numpy().astype(np.int64),
    }
    outs = {}
    for name, path in [("fp32", SRC), ("int8", DST)]:
        sess = ort.InferenceSession(path, providers=["CPUExecutionProvider"])
        t0 = time.time()
        for _ in range(3):
            res = sess.run(["logits"], inputs)
        dt = (time.time() - t0) / 3
        outs[name] = res[0]
        print(f"{name}: {dt*1000:.0f} ms/inference (L={inputs['input_ids'].shape[1]}, B=2)")
    diff = np.abs(outs["fp32"] - outs["int8"])
    valid = diff[outs["fp32"] > -1e3]
    print(f"max |dlogits| на валидных слотах = {valid.max():.4f}")

if __name__ == "__main__":
    main()
