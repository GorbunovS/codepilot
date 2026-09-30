"""Export convaiinnovations/laya-multilingual to ONNX with fixed seq/options shapes
for DirectML compatibility.

Dynamic batch stays dynamic; sequence length and number of options are fixed
at max_len/head_max_len from rl_agent_config.json. Caller pads/truncates inputs.
"""
import json
import os
import shutil
import sys

import numpy as np
import torch

MODEL_DIR = sys.argv[1] if len(sys.argv) > 1 else os.environ["LAYA_MODEL_DIR"]
OUT_DIR = os.path.abspath(sys.argv[2] if len(sys.argv) > 2 else "models/laya-multilingual")
os.makedirs(OUT_DIR, exist_ok=True)

from safetensors.torch import load_file
from laya.common import build_model

cfg = json.load(open(os.path.join(MODEL_DIR, "rl_agent_config.json")))
model = build_model(cfg, encoder_dir=os.path.join(MODEL_DIR, "encoder"), pretrained=False)
model.load_state_dict(load_file(os.path.join(MODEL_DIR, "model.safetensors")), strict=True)
model.eval()

MAX_LEN = cfg.get("max_len", 1024)
HEAD_MAX_LEN = cfg.get("head_max_len", 6)


class Wrapper(torch.nn.Module):
    def __init__(self, m):
        super().__init__()
        self.m = m

    def forward(self, input_ids, attention_mask, marker_pos, marker_mask, qtype):
        return self.m(input_ids, attention_mask, marker_pos, marker_mask, qtype)


w = Wrapper(model)
B, L, K = 20, MAX_LEN, HEAD_MAX_LEN
ex = (
    torch.randint(5, 1000, (B, L)),
    torch.ones(B, L, dtype=torch.long),
    torch.tensor([[3, 9, 15, 21] + [0] * (K - 4) for _ in range(B)]),
    torch.tensor([[True] * 4 + [False] * (K - 4) for _ in range(B)]),
    torch.tensor([i % 3 for i in range(B)]),
)
for b in range(1, B):
    ex[1][b, 30 + b * 10:] = 0

out = os.path.join(OUT_DIR, "laya.static.onnx")
batch = torch.export.Dim("batch")
prog = torch.onnx.export(
    w, ex, opset_version=18, dynamo=True, optimize=True,
    input_names=["input_ids", "attention_mask", "marker_pos", "marker_mask", "qtype"],
    output_names=["logits", "act_logits"],
    dynamic_shapes={"input_ids": {0: batch}, "attention_mask": {0: batch},
                    "marker_pos": {0: batch}, "marker_mask": {0: batch},
                    "qtype": {0: batch}},
)
prog.save(out, external_data=True)

shutil.copy(os.path.join(MODEL_DIR, "tokenizer", "tokenizer.json"),
            os.path.join(OUT_DIR, "tokenizer.json"))
shutil.copy(os.path.join(MODEL_DIR, "tokenizer", "tokenizer_config.json"),
            os.path.join(OUT_DIR, "tokenizer_config.json"))
json.dump({k: cfg[k] for k in ("max_len", "head_max_len", "temperature", "temperature_by_options")},
          open(os.path.join(OUT_DIR, "laya_config.json"), "w"), indent=1)

import onnxruntime as ort

with torch.no_grad():
    ref_logits, ref_act = w(*ex)
sess = ort.InferenceSession(out, providers=["CPUExecutionProvider"])
o = sess.run(None, {"input_ids": ex[0].numpy(), "attention_mask": ex[1].numpy(),
                    "marker_pos": ex[2].numpy(), "marker_mask": ex[3].numpy(),
                    "qtype": ex[4].numpy()})
print("max |dlogits| =", np.abs(o[0] - ref_logits.numpy()).max(),
      " max |dact| =", np.abs(o[1] - ref_act.numpy()).max())
print("wrote", out)
for f in sorted(os.listdir(OUT_DIR)):
    print(" ", f, os.path.getsize(os.path.join(OUT_DIR, f)))
