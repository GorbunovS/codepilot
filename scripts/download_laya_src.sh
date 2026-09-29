#!/bin/sh
# Скачивание исходного чекпоинта Laya (PyTorch) для экспорта в ONNX.
# Результат: models/laya-multilingual-src/ (encoder/, tokenizer/,
# model.safetensors, rl_agent_config.json). Экспорт: tools/laya-export/export_onnx.py.
set -eu

SRC_DIR="models/laya-multilingual-src"
BASE="https://huggingface.co/convaiinnovations/laya-multilingual/resolve/main"

mkdir -p "$SRC_DIR/encoder" "$SRC_DIR/tokenizer"
dl() {
  if [ -s "$2" ]; then echo "есть: $2"; else
    echo "скачиваю $1 -> $2"
    for i in 1 2 3 4 5 6 7 8; do
      curl -fL -C - --retry 5 --retry-all-errors -o "$2.tmp" "$1" && break
      echo "обрыв, повтор $i..." >&2
      sleep 3
    done
    mv "$2.tmp" "$2"
  fi
}

dl "$BASE/rl_agent_config.json" "$SRC_DIR/rl_agent_config.json"
dl "$BASE/encoder/config.json" "$SRC_DIR/encoder/config.json"
dl "$BASE/tokenizer/tokenizer.json" "$SRC_DIR/tokenizer/tokenizer.json"
dl "$BASE/tokenizer/tokenizer_config.json" "$SRC_DIR/tokenizer/tokenizer_config.json"
dl "$BASE/model.safetensors" "$SRC_DIR/model.safetensors"
echo "готово: $SRC_DIR"
