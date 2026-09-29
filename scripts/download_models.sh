#!/bin/sh
# Скачивание моделей для codepilot (не хранятся в репо, см. .gitignore).
#
#   ./scripts/download_models.sh           # e5-small fp32 (эмбеддинги)
#   ./scripts/download_models.sh int8      # e5-small int8 (118 МБ вместо 470 МБ)
#
# Источник: https://huggingface.co/Xenova/multilingual-e5-small
set -eu

E5_DIR="models/e5-small"
BASE="https://huggingface.co/Xenova/multilingual-e5-small/resolve/main"

variant="${1:-fp32}"
case "$variant" in
  fp32) MODEL_FILE="onnx/model.onnx" ;;
  int8) MODEL_FILE="onnx/model_int8.onnx" ;;
  *) echo "использование: $0 [fp32|int8]" >&2; exit 2 ;;
esac

mkdir -p "$E5_DIR"
dl() { # dl <url> <dest>
  if [ -s "$2" ]; then echo "есть: $2"; else
    echo "скачиваю $1 -> $2"
    curl -fL --retry 3 -o "$2.tmp" "$1" && mv "$2.tmp" "$2"
  fi
}

dl "$BASE/tokenizer.json" "$E5_DIR/tokenizer.json"
dl "$BASE/config.json" "$E5_DIR/config.json"
dl "$BASE/$MODEL_FILE" "$E5_DIR/model.onnx"
echo "готово: $E5_DIR (вариант $variant)"
