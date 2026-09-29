#!/usr/bin/env bash
# build_app.sh — сборка десктоп-обёртки Pake поверх веб-панели codepilot.
#
# ВАЖНО: Pake-обёртка — это лишь окно браузера поверх HTTP-сервера панели.
# Перед запуском собранного приложения должен работать `codepilot web`
# (по умолчанию на 127.0.0.1:8080). Само приложение бэкенд не поднимает.
#
# Требования: Node.js ≥ 20, pnpm, Rust (Pake собирает через Tauri;
# при первом запуске предложит установить автоматически).
set -euo pipefail
cd "$(dirname "$0")/.."

ADDR="${ADDR:-127.0.0.1:8080}"

# 1. Собираем CLI-бинарь (им нужен и `web`, и `serve` из MCP-сниппета).
go build -o codepilot ./cmd/codepilot

# 2. Собираем десктоп-обёртку (требует запущенный `codepilot web` на $ADDR
#    на момент сборки — Pake при сборке обращается к URL за иконкой/метаданными).
pnpm dlx pake-cli "http://${ADDR}" \
  --name CodePilot \
  --width 1280 \
  --height 840

echo
echo "Готово. Перед запуском приложения поднимите панель: ./codepilot web --addr ${ADDR}"
