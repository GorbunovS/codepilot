"""Export convaiinnovations/laya-multilingual to ONNX.

Adapted from https://github.com/receptron/laya/blob/main/export/export_onnx.py
to use the pip `laya` package (laya.common.build_model) instead of the
training repo's rl_common.

Inputs : input_ids [B,L] int64, attention_mask [B,L] int64, marker_pos [B,K] int64,
         marker_mask [B,K] bool, qtype [B] int64
Outputs: logits [B,K] float32 (uncalibrated; masked slots = -1e4),
         act_logits [B,2] float32 (raw; softmax is applied client-side — this is the
         naming the pip `laya` ONNXAgent expects. The older receptron/laya-onnx export
         applied softmax in-graph and named it `act_probs`.)
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


class Wrapper(torch.nn.Module):
    def __init__(self, m):
        super().__init__()
        self.m = m

    def forward(self, input_ids, attention_mask, marker_pos, marker_mask, qtype):
        return self.m(input_ids, attention_mask, marker_pos, marker_mask, qtype)


w = Wrapper(model)
B, L, K = 2, 40, 4
ex = (
    torch.randint(5, 1000, (B, L)),
    torch.ones(B, L, dtype=torch.long),
    torch.tensor([[3, 9, 15, 21], [3, 9, 0, 0]]),
    torch.tensor([[True, True, True, True], [True, True, False, False]]),
    torch.tensor([0, 2]),
)
ex[1][1, 30:] = 0  # second row padded

out = os.path.join(OUT_DIR, "laya.onnx")
batch = torch.export.Dim("batch")
seq = torch.export.Dim("seq", min=8)
opts = torch.export.Dim("options", min=2)
prog = torch.onnx.export(
    w, ex, opset_version=18, dynamo=True, optimize=True,
    input_names=["input_ids", "attention_mask", "marker_pos", "marker_mask", "qtype"],
    output_names=["logits", "act_logits"],
    dynamic_shapes={"input_ids": {0: batch, 1: seq}, "attention_mask": {0: batch, 1: seq},
                    "marker_pos": {0: batch, 1: opts}, "marker_mask": {0: batch, 1: opts},
                    "qtype": {0: batch}},
)
prog.save(out, external_data=True)

# ship the tokenizer + calibration config next to the graph
shutil.copy(os.path.join(MODEL_DIR, "tokenizer", "tokenizer.json"),
            os.path.join(OUT_DIR, "tokenizer.json"))
shutil.copy(os.path.join(MODEL_DIR, "tokenizer", "tokenizer_config.json"),
            os.path.join(OUT_DIR, "tokenizer_config.json"))
json.dump({k: cfg[k] for k in ("max_len", "head_max_len", "temperature", "temperature_by_options")},
          open(os.path.join(OUT_DIR, "laya_config.json"), "w"), indent=1)

# parity check on the export dummy batch
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
