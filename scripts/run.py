#!/usr/bin/env python3
"""Однострочный запуск codepilot на Windows.

Клонируй репо и запусти:
    python scripts/run.py

Скрипт сделает всё сам:
- скачает ONNX Runtime
- соберёт codepilot.exe
- скачает модели e5-small и Laya
- установит/запустит PostgreSQL + pgvector
- запустит веб-панель и откроет браузер
"""
from __future__ import annotations

import os
import platform
import shutil
import subprocess
import sys
import time
import urllib.request
import webbrowser
from pathlib import Path

if platform.system() != "Windows":
    print("Этот скрипт для Windows. На маке/Linux используй ./codepilot web.")
    sys.exit(1)

ROOT = Path(__file__).resolve().parent.parent
ADDR = "127.0.0.1:8080"
URL = f"http://{ADDR}"


def run(cmd: list[str | Path], **kwargs):
    print("  $", " ".join(str(c) for c in cmd))
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def download(url: str, dest: Path):
    print(f"скачиваю {url} -> {dest}")
    dest.parent.mkdir(parents=True, exist_ok=True)
    urllib.request.urlretrieve(url, dest)


def go_bin() -> Path:
    # Сначала ищем go в PATH
    go_path = shutil.which("go")
    if go_path:
        return Path(go_path)
    # Иначе используем portable Go из ~/.codepilot/go
    portable = Path.home() / ".codepilot" / "go" / "bin" / "go.exe"
    if portable.exists():
        return portable
    return None


def ensure_go() -> Path:
    g = go_bin()
    if g:
        print(f"Go найден: {g}")
        return g

    print("Go не найден в PATH. Скачиваю portable Go...")
    import tempfile, zipfile
    version = "1.26.0"
    tmp = Path(tempfile.gettempdir())
    zip_path = tmp / f"go{version}.windows-amd64.zip"
    if not zip_path.exists():
        url = f"https://go.dev/dl/go{version}.windows-amd64.zip"
        download(url, zip_path)
    go_dir = Path.home() / ".codepilot" / "go"
    if go_dir.exists():
        shutil.rmtree(go_dir)
    go_dir.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(zip_path, "r") as z:
        z.extractall(go_dir.parent)
    # после распаковки папка go/
    exe = go_dir / "bin" / "go.exe"
    if not exe.exists():
        raise RuntimeError("portable Go не распаковался")
    return exe


def ensure_codepilot():
    exe = ROOT / "codepilot.exe"
    if exe.exists():
        print("codepilot.exe уже есть")
        return
    print("Собираю codepilot.exe...")
    go = ensure_go()
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    env["GOTOOLCHAIN"] = "local"
    env.setdefault("GOPROXY", "https://proxy.golang.org,direct")
    run([go, "build", "-o", "codepilot.exe", "./cmd/codepilot"], cwd=ROOT, env=env)


def ensure_ort():
    from setup_postgres import pg_ready  # reuse

    dll = ROOT / "bin" / "onnxruntime.dll"
    if dll.exists():
        print("ONNX Runtime уже есть")
        return

    print("Скачиваю ONNX Runtime для Windows...")
    import tempfile, shutil, zipfile
    version = "1.23.1"
    tmp = Path(tempfile.gettempdir())
    zip_path = tmp / f"onnxruntime-win-x64-{version}.zip"
    if not zip_path.exists():
        url = f"https://github.com/microsoft/onnxruntime/releases/download/v{version}/onnxruntime-win-x64-{version}.zip"
        download(url, zip_path)
    extract = tmp / f"ort-win-{version}"
    if extract.exists():
        shutil.rmtree(extract)
    with zipfile.ZipFile(zip_path, "r") as z:
        z.extractall(extract)
    src = extract / f"onnxruntime-win-x64-{version}" / "lib"
    (ROOT / "bin").mkdir(parents=True, exist_ok=True)
    for f in src.glob("onnxruntime*"):
        shutil.copy2(f, ROOT / "bin" / f.name)


def ensure_models():
    from setup_models import ensure_e5, ensure_laya
    ensure_e5()
    ensure_laya()


def ensure_postgres():
    from setup_postgres import pg_ready
    if pg_ready():
        print("Postgres уже запущен")
        return
    print("Устанавливаю PostgreSQL + pgvector...")
    subprocess.run([sys.executable, str(ROOT / "scripts" / "setup_postgres.py")], check=True)


def wait_for_web():
    for i in range(120):
        try:
            urllib.request.urlopen(f"{URL}/api/projects", timeout=1)
            return True
        except Exception:
            time.sleep(0.5)
    return False


def main():
    print("=" * 50)
    print("CodePilot Windows runner")
    print("=" * 50)

    ensure_codepilot()
    ensure_ort()
    ensure_models()
    ensure_postgres()

    env = os.environ.copy()
    env["CODEPILOT_STORE"] = "pg"
    env["CODEPILOT_EMBED"] = "onnx"
    env["CODEPILOT_LAYA"] = "onnx"
    config = Path.home() / ".codepilot" / "config.json"
    if config.exists():
        import json
        try:
            cfg = json.loads(config.read_text(encoding="utf-8"))
            if "pg_dsn" in cfg:
                env["CODEPILOT_PG_DSN"] = cfg["pg_dsn"]
        except Exception:
            pass

    print("Запускаю codepilot web...")
    proc = subprocess.Popen(
        [str(ROOT / "codepilot.exe"), "web", "--addr", ADDR],
        env=env,
        cwd=ROOT,
    )

    print("Жду готовности панели...")
    if not wait_for_web():
        print("ERROR: панель не поднялась")
        proc.terminate()
        sys.exit(1)

    print(f"Открываю {URL}")
    webbrowser.open(URL)

    try:
        proc.wait()
    except KeyboardInterrupt:
        proc.terminate()
        proc.wait()


if __name__ == "__main__":
    main()
