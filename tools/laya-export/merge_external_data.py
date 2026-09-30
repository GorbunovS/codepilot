#!/usr/bin/env python3
"""Склейка Laya ONNX в однофайловую модель (laya.single.onnx).

CoreML EP не умеет читать модели с внешними весами (laya.onnx + laya.onnx.data):
при создании сессии падает с "open file ... Not a directory". Для --device coreml
нужен единый файл. 1.29 ГБ влезает в лимит protobuf (2 ГБ).

Использование:
    tools/laya-export/.venv/bin/python tools/laya-export/merge_external_data.py [model_dir]
"""

import sys
from pathlib import Path

import onnx


def main() -> None:
    model_dir = Path(sys.argv[1] if len(sys.argv) > 1 else "models/laya-multilingual")
    src_name = sys.argv[2] if len(sys.argv) > 2 else "laya.onnx"
    dst_name = sys.argv[3] if len(sys.argv) > 3 else "laya.single.onnx"
    src = model_dir / src_name
    dst = model_dir / dst_name
    if not src.exists():
        sys.exit(f"нет {src}")
    print(f"загружаю {src} (+ внешние веса)...")
    model = onnx.load(str(src))
    print(f"пишу {dst} ...")
    onnx.save_model(model, str(dst), save_as_external_data=False)
    print(f"готово: {dst} ({dst.stat().st_size / 2**30:.2f} ГБ)")


if __name__ == "__main__":
    main()
