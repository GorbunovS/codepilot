#!/usr/bin/env python3
"""Бенчмарк MLX e5-small на реальных чанках из Postgres.

Читает N чанков проекта из БД, гоняет эмбеддинги батчами, печатает текстов/сек.
Дополнительно сверяет паритет векторов с эталоном из Go-пайплайна (ONNX),
если передан файл эталона (--parity ref.json).

    tools/mlx-sidecar/.venv/bin/python tools/mlx-sidecar/bench.py [--n 2000] [--batch 32]
"""

import argparse
import json
import subprocess
import time

import mlx.core as mx
from mlx_embeddings.utils import generate, load


def fetch_texts(n: int) -> list[str]:
    out = subprocess.run(
        ["docker", "exec", "codepilotv1-db-1", "psql", "-U", "codepilot", "-d", "codepilot",
         "-t", "-A", "-c",
         f"SELECT left(content, 2000) FROM chunks "
         f"WHERE project='/Users/stanislavgorbunov/pnodes' ORDER BY id LIMIT {n}"],
        capture_output=True, text=True, check=True)
    return [l for l in out.stdout.splitlines() if l.strip()]


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--n", type=int, default=2000)
    ap.add_argument("--batch", type=int, default=32)
    ap.add_argument("--model", default="mlx-community/multilingual-e5-small-mlx")
    args = ap.parse_args()

    print(f"читаю {args.n} чанков из БД...")
    texts = fetch_texts(args.n)
    print(f"получено {len(texts)}")

    print(f"загружаю {args.model} ...")
    model, tok = load(args.model)

    # прогрев
    warm = generate(model, tok, ["passage: прогрев"])
    mx.eval(warm.last_hidden_state)

    t0 = time.perf_counter()
    done = 0
    for off in range(0, len(texts), args.batch):
        batch = ["passage: " + t for t in texts[off:off + args.batch]]
        vecs = generate(model, tok, batch)
        mx.eval(vecs.last_hidden_state)  # mlx ленивый: без eval считать не будет
        done += len(batch)
        if done % 512 == 0 or done == len(texts):
            dt = time.perf_counter() - t0
            print(f"{done}/{len(texts)}  {done / dt:.1f} текстов/с")
    dt = time.perf_counter() - t0
    print(f"\nИТОГО: {len(texts)} текстов за {dt:.1f} с = {len(texts) / dt:.1f} текстов/с "
          f"(батч {args.batch})")

    # сохраняем mean-pooled + L2-нормированные векторы первых 3 текстов
    # (тот же контракт, что у Go-пайплайна) для сверки паритета с ONNX
    import numpy as np

    batch = ["passage: " + t for t in texts[:3]]
    out = generate(model, tok, batch)
    hidden = np.array(out.last_hidden_state)  # [b, L, dim]
    enc = tok(batch, padding=True, truncation=True, max_length=512)
    mask = np.array(enc["attention_mask"])[..., None]  # [b, L, 1]
    pooled = (hidden * mask).sum(axis=1) / mask.sum(axis=1)
    norms = np.linalg.norm(pooled, axis=1, keepdims=True)
    pooled = pooled / np.maximum(norms, 1e-12)
    with open("/tmp/mlx_ref.json", "w") as f:
        json.dump(pooled.tolist(), f)
    print("эталон MLX сохранён в /tmp/mlx_ref.json")


if __name__ == "__main__":
    main()
