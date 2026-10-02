#!/usr/bin/env python3
"""Сборка macOS-установщика codepilot (.pkg).

Что внутри:
- CodePilot.app: Go-бинарь, bin/libonnxruntime.dylib, models/e5-small,
  скрипты первичной настройки, иконка, лаунчер.
- postinstall: снимает карантин и чинит владельца.

При первом запуске лаунчер:
- ставит Postgres (Docker pgvector/pgvector:pg17 → brew postgresql@17),
- копирует вшитый e5-small в ~/.codepilot/models/,
- скачивает готовый Laya ONNX с HuggingFace в ~/.codepilot/models/,
- запускает web-панель и открывает браузер.

Сборка:
    python3 scripts/build_macos.py

Результат: installers/mac/CodePilot-macOS.pkg (gitignored).
"""
from __future__ import annotations

import argparse
import json
import os
import plistlib
import shutil
import stat
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
APP_NAME = "CodePilot"
BUNDLE_ID = "com.codepilot.app"
DEFAULT_VERSION = ""  # авто: дата сборки YYYY.MM.DD-HHMM
ADDR = "127.0.0.1:8080"
REPO = "GorbunovS/codepilot"


def run(cmd: list[str | Path], **kwargs):
    print("  $", " ".join(str(c) for c in cmd))
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def parse_args():
    p = argparse.ArgumentParser()
    p.add_argument("--version", default=DEFAULT_VERSION, help="версия сборки (пусто = авто YYYY.MM.DD-HHMM)")
    return p.parse_args()


def build_go_binary():
    print("Собираю codepilot (darwin/arm64)...")
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = "darwin"
    env["GOARCH"] = "arm64"
    run(["go", "build", "-o", "codepilot_macos", "./cmd/codepilot"], cwd=ROOT, env=env)


def make_icon(work: Path) -> Path | None:
    src = ROOT / "logo.png"
    if not src.exists():
        print("WARNING: logo.png не найден, иконка стандартная")
        return None
    iconset = work / "logo.iconset"
    iconset.mkdir(parents=True, exist_ok=True)
    for size in (16, 32, 128, 256, 512):
        run(["sips", "-z", str(size), str(size), str(src),
             "--out", str(iconset / f"icon_{size}x{size}.png")],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        run(["sips", "-z", str(size * 2), str(size * 2), str(src),
             "--out", str(iconset / f"icon_{size}x{size}@2x.png")],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    icns = work / "logo.icns"
    run(["iconutil", "-c", "icns", str(iconset), "-o", str(icns)])
    return icns


LAUNCHER = r"""#!/bin/bash
# CodePilot launcher: проверка обновлений -> настройка окружения -> web -> браузер.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
RES="$HERE/../Resources"
HOME_DIR="$HOME/.codepilot"
mkdir -p "$HOME_DIR"
LOG="$HOME_DIR/launcher.log"
exec > >(tee -a "$LOG") 2>&1

echo "=== CodePilot launcher $(date) ==="

PY3="$(command -v python3 || true)"
if [ -z "$PY3" ]; then
  osascript -e 'display alert "CodePilot: нужен python3" message "Установи Xcode Command Line Tools: xcode-select --install"' || true
  exit 1
fi

# Проверка обновлений через GitHub Releases.
REPO="__REPO__"
CURRENT="$(cat "$RES/version.txt" 2>/dev/null || echo 'unknown')"
if [ "$CURRENT" != "unknown" ]; then
  LATEST="$(curl -s --max-time 5 "https://api.github.com/repos/$REPO/releases/latest" | \
    "$PY3" -c 'import json,sys; print(json.load(sys.stdin).get("tag_name",""))' 2>/dev/null || true)"
  # Нормализуем: убираем префикс v, сравниваем как версии (YYYY.MM.DD-HHMM)
  norm() { echo "$1" | sed 's/^v//'; }
  ver_gt() { [ "$(printf '%s\n' "$1" "$2" | sort -V | head -n1)" != "$1" ]; }
  if [ -n "$LATEST" ]; then
    CURRENT_NORM="$(norm "$CURRENT")"
    LATEST_NORM="$(norm "$LATEST")"
    if [ "$LATEST_NORM" != "$CURRENT_NORM" ] && ver_gt "$LATEST_NORM" "$CURRENT_NORM"; then
      osascript -e "display alert \"CodePilot: доступна версия $LATEST\" \
        message \"Установлена: $CURRENT. Скачай новый .pkg с github.com/$REPO/releases/latest\" \
        buttons {\"Позже\", \"Скачать\"} default button \"Скачать\"" 2>/dev/null | \
        grep -q "Скачать" && open "https://github.com/$REPO/releases/latest"
    fi
  fi
fi

# Postgres: docker -> brew (scripts/setup_postgres.py решит сам).
if ! nc -z 127.0.0.1 5432 2>/dev/null; then
  echo "настраиваю Postgres..."
  "$PY3" "$RES/scripts/setup_postgres.py" || {
    osascript -e 'display alert "CodePilot: Postgres не настроен" message "См. ~/.codepilot/launcher.log. Нужен Docker Desktop или Homebrew."' || true
    exit 1
  }
fi

# Модели: e5 копируем из бандла в пользовательскую папку,
# Laya докачивается туда же при первом запуске.
MODELS_DIR="$HOME_DIR/models"
mkdir -p "$MODELS_DIR"
export CODEPILOT_MODELS_DIR="$MODELS_DIR"
if [ -d "$RES/models/e5-small" ] && [ ! -f "$MODELS_DIR/e5-small/model.onnx" ]; then
  echo "копирую e5-small в $MODELS_DIR..."
  rm -rf "$MODELS_DIR/e5-small"
  cp -R "$RES/models/e5-small" "$MODELS_DIR/e5-small"
fi

echo "проверяю модели..."
"$PY3" "$RES/scripts/setup_models.py" || {
  osascript -e 'display alert "CodePilot: модели не скачались" message "См. ~/.codepilot/launcher.log"' || true
  exit 1
}

export CODEPILOT_STORE=pg
export CODEPILOT_EMBED=onnx
export CODEPILOT_LAYA=onnx
export CODEPILOT_ONNXRUNTIME_DLL="$RES/bin/libonnxruntime.dylib"

# DSN из конфига (его пишет setup_postgres.py)
DSN="$("$PY3" -c 'import json,os;print(json.load(open(os.path.expanduser("~/.codepilot/config.json"))).get("pg_dsn",""))' 2>/dev/null || true)"
if [ -n "$DSN" ]; then
  export CODEPILOT_PG_DSN="$DSN"
fi

"$RES/bin/codepilot" web --addr __ADDR__ --embed-dir "$MODELS_DIR/e5-small" --laya-dir "$MODELS_DIR/laya-multilingual" &
WEB_PID=$!
trap 'kill $WEB_PID 2>/dev/null || true' EXIT

for i in $(seq 1 120); do
  if curl -s "http://__ADDR__/api/projects" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

open "http://__ADDR__"
wait $WEB_PID
"""


def assemble_app(work: Path) -> Path:
    app = work / f"{APP_NAME}.app"
    macos = app / "Contents" / "MacOS"
    res = app / "Contents" / "Resources"
    macos.mkdir(parents=True)
    res.mkdir(parents=True)

    # бинарь и либы
    shutil.copy2(ROOT / "codepilot_macos", res / "bin_codepilot_tmp")
    (res / "bin").mkdir()
    shutil.move(str(res / "bin_codepilot_tmp"), res / "bin" / "codepilot")
    dylib = ROOT / "bin" / "libonnxruntime.dylib"
    real = ROOT / "bin" / "libonnxruntime.1.23.1.dylib"
    if real.exists():
        shutil.copy2(real, res / "bin" / real.name)
        os.symlink(real.name, res / "bin" / "libonnxruntime.dylib")
    elif dylib.exists():
        shutil.copy2(dylib, res / "bin" / "libonnxruntime.dylib")

    # e5-small вшиваем (470 МБ), Laya докачивается при первом запуске
    e5 = ROOT / "models" / "e5-small"
    if e5.exists():
        shutil.copytree(e5, res / "models" / "e5-small")
    else:
        print("WARNING: models/e5-small не найден — скачается при первом запуске")

    # скрипты первичной настройки
    sdir = res / "scripts"
    sdir.mkdir()
    for name in ("setup_postgres.py", "setup_models.py"):
        shutil.copy2(ROOT / "scripts" / name, sdir / name)

    # лаунчер
    launcher = macos / APP_NAME
    launcher.write_text(
        LAUNCHER.replace("__ADDR__", ADDR).replace("__REPO__", REPO),
        encoding="utf-8",
    )
    launcher.chmod(launcher.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

    # версия для проверки обновлений
    (res / "version.txt").write_text(VERSION, encoding="utf-8")

    # иконка
    icon = make_icon(work)

    plist = {
        "CFBundleName": APP_NAME,
        "CFBundleDisplayName": APP_NAME,
        "CFBundleIdentifier": BUNDLE_ID,
        "CFBundleVersion": VERSION,
        "CFBundleShortVersionString": VERSION,
        "CFBundleExecutable": APP_NAME,
        "CFBundlePackageType": "APPL",
        "LSMinimumSystemVersion": "12.0",
        "NSHighResolutionCapable": True,
    }
    if icon:
        shutil.copy2(icon, res / "logo.icns")
        plist["CFBundleIconFile"] = "logo"
    with open(app / "Contents" / "Info.plist", "wb") as f:
        plistlib.dump(plist, f)
    return app


def build_pkg(app: Path, work: Path) -> Path:
    payload = work / "payload"
    payload.mkdir()
    shutil.move(str(app), payload / app.name)

    scripts = work / "pkg-scripts"
    scripts.mkdir()
    post = scripts / "postinstall"
    post.write_text(
        "#!/bin/bash\n"
        "# Снимаем карантин (пакет не подписан Developer ID) и чиним владельца.\n"
        "xattr -dr com.apple.quarantine /Applications/CodePilot.app 2>/dev/null || true\n"
        "exit 0\n",
        encoding="utf-8",
    )
    post.chmod(0o755)

    out = ROOT / "installers" / "mac" / "CodePilot-macOS.pkg"
    out.parent.mkdir(parents=True, exist_ok=True)
    run([
        "pkgbuild",
        "--root", str(payload),
        "--scripts", str(scripts),
        "--identifier", BUNDLE_ID,
        "--version", VERSION,
        "--install-location", "/Applications",
        str(out),
    ])
    # JSON-метаданные для CI / ручной проверки
    meta = {
        "version": VERSION,
        "download_url": f"https://github.com/{REPO}/releases/latest/download/CodePilot-macOS.pkg",
        "release_url": f"https://github.com/{REPO}/releases/latest",
    }
    (out.parent / "version.json").write_text(
        json.dumps(meta, indent=2, ensure_ascii=False), encoding="utf-8"
    )
    return out


def main():
    args = parse_args()
    global VERSION
    VERSION = args.version
    if not VERSION:
        from datetime import datetime
        VERSION = datetime.now().strftime("%Y.%m.%d-%H%M")
        print(f"автоверсия: {VERSION}")
    if sys.platform != "darwin":
        print("сборка macOS-установщика возможна только на macOS")
        sys.exit(1)
    build_go_binary()
    with tempfile.TemporaryDirectory() as td:
        work = Path(td)
        app = assemble_app(work)
        pkg = build_pkg(app, work)
    (ROOT / "codepilot_macos").unlink(missing_ok=True)
    size_mb = pkg.stat().st_size / 2**20
    print(f"\nГотово: {pkg} ({size_mb:.0f} МБ)")
    print("Коллеге: двойной клик по .pkg → установка в /Applications →")
    print("запуск CodePilot из Launchpad. При первом запуске поставится")
    print("Postgres (Docker или brew) и скачается модель Laya.")


if __name__ == "__main__":
    main()
