#!/usr/bin/env python3
"""Сборка портативной десктоп-папки и установщика codepilot.

Поддерживает нативную сборку и кросс-сборку Windows-установщика на маке/Linux.

Запуск:
    python scripts/build_desktop.py              # нативная сборка
    python scripts/build_desktop.py --target windows   # Windows .exe на маке
"""
from __future__ import annotations

import argparse
import os
import platform
import shutil
import subprocess
import sys
import tempfile
import urllib.request
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ORT_VERSION = "1.23.1"
NATIVE_OS = platform.system()


def parse_args():
    p = argparse.ArgumentParser()
    p.add_argument("--target", choices=["native", "windows"], default="native",
                   help="целевая платформа (native — текущая ОС)")
    p.add_argument("--include-models", action="store_true",
                   help="включить models/ в папку установщика (иначе скачиваются при первом запуске)")
    p.add_argument("--include-postgres", action="store_true",
                   help="включить PostgreSQL-бинарники в папку установщика (иначе скачиваются при первом запуске)")
    return p.parse_args()


def target_os(args) -> str:
    return NATIVE_OS if args.target == "native" else "Windows"


def out_dir(args) -> Path:
    return ROOT / f"CodePilot-{target_os(args)}"


def run(cmd: list[str | Path], **kwargs):
    print("  $", " ".join(str(c) for c in cmd))
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def download(url: str, dest: Path):
    print(f"скачиваю {url} -> {dest}")
    dest.parent.mkdir(parents=True, exist_ok=True)
    urllib.request.urlretrieve(url, dest)


def ensure_models():
    setup = ROOT / "scripts" / "setup_models.py"
    if setup.exists():
        run([sys.executable, str(setup)], cwd=ROOT)


def ensure_ort_windows():
    dll = ROOT / "bin" / "onnxruntime.dll"
    if dll.exists():
        print("есть onnxruntime.dll")
        return
    print("Скачиваю ONNX Runtime для Windows...")
    tmp = Path(tempfile.gettempdir())
    zip_path = tmp / f"onnxruntime-win-x64-{ORT_VERSION}.zip"
    if not zip_path.exists():
        url = (
            f"https://github.com/microsoft/onnxruntime/releases/download/"
            f"v{ORT_VERSION}/onnxruntime-win-x64-{ORT_VERSION}.zip"
        )
        download(url, zip_path)
    extract = tmp / f"ort-win-{ORT_VERSION}"
    if extract.exists():
        shutil.rmtree(extract)
    extract.mkdir(parents=True, exist_ok=True)
    shutil.unpack_archive(zip_path, extract)
    src_dir = extract / f"onnxruntime-win-x64-{ORT_VERSION}" / "lib"
    (ROOT / "bin").mkdir(parents=True, exist_ok=True)
    for f in src_dir.glob("onnxruntime*"):
        shutil.copy2(f, ROOT / "bin" / f.name)
    print(f"скопировано в {ROOT / 'bin'}")


def build_binary(args):
    print("Собираю codepilot...")
    env = os.environ.copy()
    if target_os(args) == "Windows":
        env["CGO_ENABLED"] = "0"
        env["GOOS"] = "windows"
        env["GOARCH"] = "amd64"
        run(["go", "build", "-o", "codepilot.exe", "./cmd/codepilot"], cwd=ROOT, env=env)
    else:
        binary = "codepilot.exe" if NATIVE_OS == "Windows" else "codepilot"
        run(["go", "build", "-o", binary, "./cmd/codepilot"], cwd=ROOT)


def copy_tree(args):
    out = out_dir(args)
    print(f"Копирую файлы в {out}...")
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True, exist_ok=True)

    binary = "codepilot.exe" if target_os(args) == "Windows" else "codepilot"
    shutil.copy2(ROOT / binary, out / binary)

    scripts = ["launcher.py", "setup_postgres.py", "setup_models.py"]
    for name in scripts:
        src = ROOT / "scripts" / name
        if src.exists():
            shutil.copy2(src, out / name)

    # bin/ (ONNX Runtime) — лёгкий и необходимый, всегда включаем
    if (ROOT / "bin").exists():
        shutil.copytree(ROOT / "bin", out / "bin", dirs_exist_ok=True)

    # models/ — тяжёлые e5-small + Laya; по умолчанию качаются при первом запуске
    if args.include_models and (ROOT / "models").exists():
        shutil.copytree(ROOT / "models", out / "models", dirs_exist_ok=True)

    # PostgreSQL-бинарники — тяжёлые; по умолчанию качаются при первом запуске
    if args.include_postgres and (ROOT / ".codepilot" / "pgsql").exists():
        shutil.copytree(ROOT / ".codepilot" / "pgsql", out / "pgsql", dirs_exist_ok=True)


def build_launcher_exe(args):
    if target_os(args) != "Windows":
        return
    out = out_dir(args)
    try:
        subprocess.run(["pyinstaller", "--version"], capture_output=True, check=True)
    except Exception:
        print("PyInstaller не найден — пропускаю сборку CodePilot.exe")
        return

    print("Собираю CodePilot.exe через PyInstaller...")
    run([
        "pyinstaller",
        "--onefile",
        "--windowed",
        "--name", "CodePilot",
        "--distpath", str(out),
        "--workpath", str(out / "build"),
        "--specpath", str(out),
        str(out / "launcher.py"),
    ], cwd=ROOT)


def build_installer(args):
    if target_os(args) != "Windows":
        return
    try:
        subprocess.run(["makensis", "-VERSION"], capture_output=True, check=True)
    except Exception:
        print("makensis не найден — пропускаю сборку CodePilot-Setup.exe")
        return

    print("Собираю CodePilot-Setup.exe через NSIS...")
    run(["makensis", str(ROOT / "scripts" / "codepilot.nsi")], cwd=ROOT)


def main():
    args = parse_args()
    # Если модели включаются в установщик — убедимся, что они скачаны
    if args.include_models:
        ensure_models()
    if target_os(args) == "Windows":
        ensure_ort_windows()
    build_binary(args)
    copy_tree(args)
    build_launcher_exe(args)
    build_installer(args)

    out = out_dir(args)
    print(f"\nГотово: {out}")
    if target_os(args) == "Windows":
        setup = ROOT / "CodePilot-Setup.exe"
        if setup.exists():
            print(f"Установщик: {setup}")
    else:
        print("Для запуска: python launcher.py")


if __name__ == "__main__":
    main()
