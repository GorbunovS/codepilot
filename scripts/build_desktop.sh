#!/usr/bin/env bash
# build_desktop.sh — сборка десктопного CodePilot.app для macOS.
# Поверх Pake-обёртки зашиваем автозапуск codepilot web внутри .app.
# Требования: Go, pnpm, Postgres+pgvector (docker compose up -d db),
#             модели в models/ и onnxruntime в bin/.
set -euo pipefail
cd "$(dirname "$0")/.."

ADDR="${ADDR:-127.0.0.1:8080}"
APP_NAME="${APP_NAME:-CodePilot}"
APP="${APP_NAME}.app"
ICON="logo.icns"

# 1. Собираем CLI.
go build -o codepilot ./cmd/codepilot

# 1.5. Генерируем macOS-иконку из logo.png.
if [[ -f logo.png ]]; then
  echo "Генерирую $ICON из logo.png..."
  ICONSET=".tmp.logo.iconset"
  rm -rf "$ICONSET"
  mkdir -p "$ICONSET"
  for size in 16 32 128 256 512; do
    sips -z "$size" "$size" logo.png --out "$ICONSET/icon_${size}x${size}.png" >/dev/null 2>&1
    sips -z "$((size*2))" "$((size*2))" logo.png --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null 2>&1
  done
  iconutil -c icns "$ICONSET" -o "$ICON"
  rm -rf "$ICONSET"
else
  echo "WARNING: logo.png не найден, иконка будет стандартной Pake"
  ICON=""
fi

# 2. Поднимаем панель на момент сборки Pake (Pake ходит за иконкой/метаданными).
./codepilot web --addr "$ADDR" &
WEB_PID=$!
cleanup_web() { kill "$WEB_PID" 2>/dev/null || true; wait "$WEB_PID" 2>/dev/null || true; }
trap cleanup_web EXIT

# 3. Ждём готовности панели.
for i in $(seq 1 120); do
  if curl -s "http://$ADDR/api/projects" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

# 4. Собираем Pake-обёртку.
PAKE_ARGS=(
  "http://${ADDR}"
  --name "$APP_NAME"
  --width 1280
  --height 840
)
if [[ -n "$ICON" && -f "$ICON" ]]; then
  PAKE_ARGS+=(--icon "$ICON")
fi
pnpm dlx pake-cli "${PAKE_ARGS[@]}"

# 5. Останавливаем временную панель.
cleanup_web
trap - EXIT

# 6. Проверяем, что Pake создал bundle.
if [[ ! -d "$APP" ]]; then
  echo "ERROR: $APP не создан Pake"
  exit 1
fi

MACOS="$APP/Contents/MacOS"

# 7. Упаковываем бэкенд и зависимости внутрь .app.
cp codepilot "$MACOS/codepilot"
cp -R bin "$MACOS/bin"
cp -R models "$MACOS/models"

# 8. Переименовываем оригинальный Pake-бинарь.
mv "$MACOS/$APP_NAME" "$MACOS/${APP_NAME}.pake"

# 9. Создаём launcher с автозапуском бэкенда.
cat > "$MACOS/$APP_NAME" <<EOF
#!/bin/bash
set -euo pipefail
cd "\$(dirname "\$0")"

# Запускаем бэкенд codepilot web.
./codepilot web --addr 127.0.0.1:8080 &
WEB_PID=\$!

# Ждём готовности панели.
for i in \$(seq 1 120); do
  if curl -s http://127.0.0.1:8080/api/projects >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

# Запускаем UI.
./${APP_NAME}.pake
RC=\$?

# Останавливаем бэкенд при выходе.
kill \$WEB_PID 2>/dev/null || true
wait \$WEB_PID 2>/dev/null || true
exit \$RC
EOF

chmod +x "$MACOS/$APP_NAME"

echo
echo "Готово: $APP"
echo "Перед запуском убедитесь, что Postgres запущен (docker compose up -d db)."
echo "При первом запуске macOS может блокировать неподписанное приложение — открывайте через ПКМ → 'Открыть'."
