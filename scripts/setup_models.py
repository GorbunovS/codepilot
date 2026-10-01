#!/usr/bin/env python3
"""Скачивание моделей e5-small и Laya для codepilot.

Запуск:
    python scripts/setup_models.py
"""
import json
import os
import subprocess
import sys
import tempfile
import urllib.request
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MODELS_DIR = ROOT / "models"


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
    if (laya_dir / "laya.onnx").exists():
        print(f"есть: {laya_dir / 'laya.onnx'}")
        return

    src_dir = MODELS_DIR / "laya-multilingual-src"
    base = "https://huggingface.co/convaiinnovations/laya-multilingual/resolve/main"
    files = {
        "rl_agent_config.json": f"{base}/rl_agent_config.json",
        "encoder/config.json": f"{base}/encoder/config.json",
        "tokenizer/tokenizer.json": f"{base}/tokenizer/tokenizer.json",
        "tokenizer/tokenizer_config.json": f"{base}/tokenizer/tokenizer_config.json",
        "model.safetensors": f"{base}/model.safetensors",
    }
    for rel, url in files.items():
        dest = src_dir / rel
        if dest.exists():
            print(f"есть: {dest}")
            continue
        download(url, dest)

    export_script = ROOT / "tools" / "laya-export" / "export_onnx.py"
    req = ROOT / "tools" / "laya-export" / "requirements.txt"
    if export_script.exists() and req.exists():
        print("Экспортирую Laya в ONNX...")
        try:
            import laya  # noqa: F401
        except ImportError:
            print("Устанавливаю зависимости laya-export...")
            subprocess.run([sys.executable, "-m", "pip", "install", "-r", str(req)], check=True)
        subprocess.run(
            [sys.executable, str(export_script), str(src_dir), str(laya_dir)],
            check=True,
        )
    else:
        print("WARNING: не найден export_onnx.py; положите готовый Laya ONNX в models/laya-multilingual/")


if __name__ == "__main__":
    ensure_e5()
    ensure_laya()
    print("готово")
