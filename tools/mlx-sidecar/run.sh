#!/bin/bash
# Запуск MLX-сайдкара эмбеддингов (FastAPI/uvicorn на 127.0.0.1:8081).
# Модель скачивается с HuggingFace при первом старте (~0.5 ГБ, кеш ~/.cache/huggingface).
set -e
cd "$(dirname "$0")"
exec .venv/bin/python -m uvicorn server:app --host 127.0.0.1 --port 8081
