#!/usr/bin/env python3
"""Сборка macOS-установщика codepilot (.pkg).

Что внутри:
- CodePilot.app: Go-бинарь, bin/libonnxruntime.dylib, models/e5-small,
  скрипты первичной настройки, иконка, лаунчер.
- postinstall: ставит Postgres (Docker pgvector/pgvector:pg17 → brew
  postgresql@17), скачивает модели (e5 уже вшит, Laya — с HF + экспорт).

Сборка:
    python3 scripts/build_macos.py

Результат: CodePilot-macOS.pkg в корне репо.
"""
from __future__ import annotations

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
VERSION = "1.0.0"
ADDR = "127.0.0.1:8080"


def run(cmd: list[str | Path], **kwargs):
    print("  $", " ".join(str(c) for c in cmd))
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


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
# CodePilot launcher: настройка окружения -> codepilot web -> браузер.
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

# Postgres: docker -> brew (scripts/setup_postgres.py решит сам).
if ! nc -z 127.0.0.1 5432 2>/dev/null; then
  echo "настраиваю Postgres..."
  "$PY3" "$RES/scripts/setup_postgres.py" || {
    osascript -e 'display alert "CodePilot: Postgres не настроен" message "См. ~/.codepilot/launcher.log. Нужен Docker Desktop или Homebrew."' || true
    exit 1
  }
fi

# Модели (e5 вшит, Laya докачивается один раз).
echo "проверяю модели..."
cd "$RES"
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

"$RES/bin/codepilot" web --addr __ADDR__ &
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
    # setup_models.py ждёт tools/laya-export рядом с models/
    lexp = res / "tools" / "laya-export"
    lexp.mkdir(parents=True)
    for f in (ROOT / "tools" / "laya-export").glob("*.py"):
        shutil.copy2(f, lexp / f.name)
    shutil.copy2(ROOT / "tools" / "laya-export" / "requirements.txt", lexp / "requirements.txt")

    # лаунчер
    launcher = macos / APP_NAME
    launcher.write_text(LAUNCHER.replace("__ADDR__", ADDR), encoding="utf-8")
    launcher.chmod(launcher.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

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

    out = ROOT / "CodePilot-macOS.pkg"
    run([
        "pkgbuild",
        "--root", str(payload),
        "--scripts", str(scripts),
        "--identifier", BUNDLE_ID,
        "--version", VERSION,
        "--install-location", "/Applications",
        str(out),
    ])
    return out


def main():
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
