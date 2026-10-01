#!/usr/bin/env python3
"""Десктоп-лаунчер codepilot.

Запускает codepilot web в фоне, проверяет/устанавливает зависимости
(Postgres, модели) и открывает UI в окне pywebview или системном браузере.

Использование:
    python scripts/launcher.py
"""
import json
import os
import platform
import shutil
import socket
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

APP_NAME = "CodePilot"
ADDR = "127.0.0.1:8080"
URL = f"http://{ADDR}"


def root_dir() -> Path:
    # Либо папка со скриптом/родитель (при запуске из scripts/), либо папка .exe
    p = Path(__file__).resolve().parent
    if p.name == "scripts":
        return p.parent
    return p


def bin_name() -> str:
    return "codepilot.exe" if platform.system() == "Windows" else "codepilot"


def codepilot_bin() -> Path:
    return root_dir() / bin_name()


def models_dir() -> Path:
    return root_dir() / "models"


def pg_ready() -> bool:
    try:
        with socket.create_connection(("127.0.0.1", 5432), timeout=1):
            return True
    except OSError:
        return False


def web_ready() -> bool:
    try:
        urllib.request.urlopen(f"{URL}/api/projects", timeout=1)
        return True
    except Exception:
        return False


def run(cmd: list[str | Path], **kwargs):
    print("  $", " ".join(str(c) for c in cmd))
    return subprocess.Popen([str(c) for c in cmd], **kwargs)


def ensure_postgres():
    if pg_ready():
        print("Postgres уже запущен")
        return
    setup = root_dir() / "scripts" / "setup_postgres.py"
    if not setup.exists():
        setup = root_dir() / "setup_postgres.py"
    if setup.exists():
        print("Запускаю установку Postgres...")
        subprocess.run([sys.executable, str(setup)], check=True)
    else:
        print("WARNING: setup_postgres.py не найден, предполагаю внешний Postgres")


def ensure_models():
    e5 = models_dir() / "e5-small" / "model.onnx"
    laya = models_dir() / "laya-multilingual" / "laya.onnx"
    if e5.exists() and laya.exists():
        print("Модели на месте")
        return
    setup = root_dir() / "scripts" / "setup_models.py"
    if not setup.exists():
        setup = root_dir() / "setup_models.py"
    if setup.exists():
        print("Запускаю скачивание моделей...")
        subprocess.run([sys.executable, str(setup)], check=True)
    else:
        print("WARNING: setup_models.py не найден, модели нужно скачать вручную")


def load_config_dsn() -> str | None:
    home = Path.home() / ".codepilot" / "config.json"
    if not home.exists():
        return None
    try:
        cfg = json.loads(home.read_text(encoding="utf-8"))
        return cfg.get("pg_dsn")
    except Exception:
        return None


def open_window():
    try:
        import webview
        print("Открываю окно pywebview...")
        webview.create_window(APP_NAME, URL, width=1280, height=840)
        webview.start()
    except ImportError:
        print("pywebview не установлен, открываю системный браузер...")
        if platform.system() == "Windows":
            os.startfile(URL)
        elif platform.system() == "Darwin":
            subprocess.run(["open", URL])
        else:
            subprocess.run(["xdg-open", URL])


def main():
    cp = codepilot_bin()
    if not cp.exists():
        print(f"ERROR: не найден бинарь {cp}")
        sys.exit(1)

    ensure_models()
    ensure_postgres()

    env = os.environ.copy()
    env["CODEPILOT_STORE"] = "pg"
    env["CODEPILOT_EMBED"] = "onnx"
    env["CODEPILOT_LAYA"] = "onnx"
    dsn = load_config_dsn()
    if dsn:
        env["CODEPILOT_PG_DSN"] = dsn

    print("Запускаю codepilot web...")
    proc = run([cp, "web", "--addr", ADDR], env=env)

    print("Жду готовности панели...")
    for i in range(120):
        if web_ready():
            break
        time.sleep(0.5)
    else:
        print("ERROR: панель не поднялась")
        proc.terminate()
        sys.exit(1)

    try:
        open_window()
    finally:
        print("Останавливаю codepilot web...")
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()


if __name__ == "__main__":
    main()
