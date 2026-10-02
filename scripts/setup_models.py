#!/usr/bin/env python3
"""Скачивание моделей e5-small и Laya для codepilot.

Запуск:
    python scripts/setup_models.py
"""
from __future__ import annotations

import json
import os
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MODELS_DIR = Path(os.environ.get("CODEPILOT_MODELS_DIR", ROOT / "models"))


def download(url: str, dest: Path):
    print(f"скачиваю {url} -> {dest}")
    dest.parent.mkdir(parents=True, exist_ok=True)
    urllib.request.urlretrieve(url, dest)


def ensure_e5():
    e5_dir = MODELS_DIR / "e5-small"
    e5_dir.mkdir(parents=True, exist_ok=True)
    base = "https://huggingface.co/Xenova/multilingual-e5-small/resolve/main"
    files = {
        "tokenizer.json": f"{base}/tokenizer.json",
        "config.json": f"{base}/config.json",
        "model.onnx": f"{base}/onnx/model.onnx",
    }
    for name, url in files.items():
        dest = e5_dir / name
        if dest.exists():
            print(f"есть: {dest}")
            continue
        download(url, dest)


def ensure_laya():
    laya_dir = MODELS_DIR / "laya-multilingual"
    if (laya_dir / "laya.onnx").exists() and (laya_dir / "laya.onnx.data").exists():
        print(f"есть: {laya_dir / 'laya.onnx'} (+ data)")
        return

    # Готовый ONNX (без локального экспорта — не нужен torch).
    onnx_base = "https://huggingface.co/yehor-oleksiuk/laya-multilingual-onnx/resolve/main"
    files = {
        "laya.onnx": f"{onnx_base}/model_fp32.onnx",
        "laya.onnx.data": f"{onnx_base}/model_fp32.onnx.data",
        "tokenizer.json": f"{onnx_base}/tokenizer.json",
        "tokenizer_config.json": f"{onnx_base}/tokenizer_config.json",
    }
    for rel, url in files.items():
        dest = laya_dir / rel
        if dest.exists():
            print(f"есть: {dest}")
            continue
        download(url, dest)

    # Конфиг калибровки из исходного чекпоинта.
    src_cfg = MODELS_DIR / "laya-multilingual-src" / "rl_agent_config.json"
    if not src_cfg.exists():
        download(
            "https://huggingface.co/convaiinnovations/laya-multilingual/resolve/main/rl_agent_config.json",
            src_cfg,
        )
    cfg = json.load(open(src_cfg))
    json.dump(
        {k: cfg[k] for k in ("max_len", "head_max_len", "temperature", "temperature_by_options")},
        open(laya_dir / "laya_config.json", "w"),
        indent=1,
    )


if __name__ == "__main__":
    ensure_e5()
    ensure_laya()
    print("готово")
