import onnxruntime as ort
import numpy as np

sess = ort.InferenceSession("models/laya-multilingual/laya.static.onnx", providers=["DmlExecutionProvider"])
B, L, K = 20, 1024, 256
inputs = {
    "input_ids": np.random.randint(5, 1000, (B, L)).astype(np.int64),
    "attention_mask": np.ones((B, L), dtype=np.int64),
    "marker_pos": np.tile([3, 9, 15, 21] + [0] * (K - 4), (B, 1)).astype(np.int64),
    "marker_mask": np.zeros((B, K), dtype=bool),
    "qtype": np.zeros(B, dtype=np.int64),
}
inputs["marker_mask"][:, :4] = True
out = sess.run(None, inputs)
print("OK", out[0].shape, out[1].shape)
