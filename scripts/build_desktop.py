#!/usr/bin/env python3
"""Сборка портативной десктоп-папки codepilot.

Создаёт директорию CodePilot-<OS>/ с бинарём, скриптами установки
Postgres/моделей и лаунчером. Модели и ONNX Runtime должны быть
предварительно скачаны (см. setup_models.py и download_ort_*.sh).

Запуск:
    python scripts/build_desktop.py
"""
import platform
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / f"CodePilot-{platform.system()}"
SCRIPTS = ["launcher.py", "setup_postgres.py", "setup_models.py"]


def run(cmd: list[str | Path], **kwargs):
    print("  $", " ".join(str(c) for c in cmd))
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def build_binary():
    print("Собираю codepilot...")
    if platform.system() == "Windows":
        run(["go", "build", "-o", "codepilot.exe", "./cmd/codepilot"], cwd=ROOT)
    else:
        run(["go", "build", "-o", "codepilot", "./cmd/codepilot"], cwd=ROOT)


def copy_tree():
    print(f"Копирую файлы в {OUT}...")
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True, exist_ok=True)

    binary = "codepilot.exe" if platform.system() == "Windows" else "codepilot"
    shutil.copy2(ROOT / binary, OUT / binary)

    for name in SCRIPTS:
        src = ROOT / "scripts" / name
        if src.exists():
            shutil.copy2(src, OUT / name)

    for d in ["bin", "models"]:
        src = ROOT / d
        if src.exists():
            shutil.copytree(src, OUT / d, dirs_exist_ok=True)


def build_launcher_exe():
    if platform.system() != "Windows":
        return
    try:
        subprocess.run(["pyinstaller", "--version"], capture_output=True, check=True)
    except Exception:
        print("PyInstaller не найден — пропускаю сборку .exe лаунчера")
        return

    print("Собираю CodePilot.exe через PyInstaller...")
    spec = OUT / "launcher.spec"
    run([
        "pyinstaller",
        "--onefile",
        "--windowed",
        "--name", "CodePilot",
        "--distpath", str(OUT),
        "--workpath", str(OUT / "build"),
        "--specpath", str(OUT),
        str(OUT / "launcher.py"),
    ], cwd=ROOT)


def main():
    build_binary()
    copy_tree()
    build_launcher_exe()
    print(f"\nГотово: {OUT}")
    print("Для запуска:\n  Windows: CodePilot.exe\n  macOS/Linux: python launcher.py")


if __name__ == "__main__":
    main()
