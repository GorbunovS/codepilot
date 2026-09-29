#!/usr/bin/env python3
"""MLX-сайдкар эмбеддингов e5-small для codepilot.

FastAPI/uvicorn-сервис на 127.0.0.1:8081; Go-клиент — internal/embed/remote.go.
Инференс через MLX (Apple Silicon): ~100x быстрее локального ONNX-пайплайна.

Контракт эмбеддингов совпадает с Go-пайплайном (internal/embed/embed.go):
префиксы "passage: "/"query: ", mean pooling по attention mask, L2-норма,
размерность 384, максимум 512 токенов.

    tools/mlx-sidecar/run.sh
    curl 127.0.0.1:8081/health
    curl -X POST 127.0.0.1:8081/embed -H 'Content-Type: application/json' \
        -d '{"texts": ["функция ValidateToken"], "prefix": "passage: "}'
"""

import numpy as np
import mlx.core as mx
from fastapi import FastAPI
from mlx_embeddings.utils import generate, load
from pydantic import BaseModel

MODEL = "mlx-community/multilingual-e5-small-mlx"
BATCH = 64  # внутренняя нарезка, если клиент прислал батч больше

print(f"загружаю {MODEL} ...")
model, tok = load(MODEL)
# прогрев: первая компиляция графа не должна попадать в боевой запрос
mx.eval(generate(model, tok, ["passage: прогрев"]).last_hidden_state)
print("модель готова")

app = FastAPI(title="codepilot mlx-sidecar")


class EmbedRequest(BaseModel):
    texts: list[str]
    prefix: str = "passage: "  # "passage: " для документов, "query: " для запросов


@app.get("/health")
def health() -> dict:
    return {"ok": True}


@app.post("/embed")
def embed(req: EmbedRequest) -> dict:
    vectors: list[list[float]] = []
    for off in range(0, len(req.texts), BATCH):
        batch = [req.prefix + t for t in req.texts[off:off + BATCH]]
        out = generate(model, tok, batch)
        hidden = np.array(out.last_hidden_state, dtype=np.float32)  # [b, L, 384]
        enc = tok(batch, padding=True, truncation=True, max_length=512)
        mask = np.array(enc["attention_mask"])[..., None]  # [b, L, 1]
        # mean pooling по маске + L2-норма (cosine = dot), как в embed.go
        pooled = (hidden * mask).sum(axis=1) / mask.sum(axis=1)
        norms = np.linalg.norm(pooled, axis=1, keepdims=True)
        pooled = pooled / np.maximum(norms, 1e-12)
        vectors.extend(pooled.tolist())
    return {"vectors": vectors}
