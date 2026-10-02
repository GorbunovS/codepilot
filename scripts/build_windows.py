#!/usr/bin/env python3
"""Сборка Windows-установщика codepilot (Inno Setup).

Что внутри:
- CodePilot.exe: Pake-окно (Tauri) поверх веб-панели + Go-бинарь,
  bin/onnxruntime.dll, models/e5-small, скрипты первичной настройки.
  Точка входа — CodePilot.exe (скомпилированный launcher.py): поднимает
  `codepilot web` и открывает окно. Настройка моделей/Postgres — после
  старта приложения (onboarding UI).
- Inno Setup-установщик: CodePilot-Setup.exe.

Требования на машине сборки: Go, Node.js + pnpm, Rust (для Pake/Tauri),
Python + PyInstaller, Inno Setup (iscc).

Сборка:
    python scripts/build_windows.py

Результат: installers/windows/CodePilot-Setup.exe (gitignored).
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
APP_NAME = "CodePilot"
DEFAULT_VERSION = ""  # авто: дата сборки YYYY.MM.DD-HHMM
ADDR = "127.0.0.1:8080"
REPO = "GorbunovS/codepilot"
ORT_VERSION = "1.23.1"


def run(cmd: list[str | Path], **kwargs):
    print("  $", " ".join(str(c) for c in cmd))
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def parse_args():
    p = argparse.ArgumentParser()
    p.add_argument("--version", default=DEFAULT_VERSION, help="версия сборки (пусто = авто YYYY.MM.DD-HHMM)")
    return p.parse_args()


def build_go_binary():
    print("Собираю codepilot.exe (windows/amd64)...")
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = "windows"
    env["GOARCH"] = "amd64"
    run(["go", "build", "-o", "codepilot_windows.exe", "./cmd/codepilot"], cwd=ROOT, env=env)


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
        print(f"скачиваю {url} -> {zip_path}")
        urllib.request.urlretrieve(url, zip_path)
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


def ensure_models():
    print("Скачиваю модели (e5-small)...")
    setup = ROOT / "scripts" / "setup_models.py"
    run([sys.executable, str(setup)], cwd=ROOT)


def find_pake_exe(pake_dir: Path) -> Path | None:
    """Ищет собранный .exe Pake в build-дереве."""
    candidates = [
        pake_dir / "src-tauri" / "target" / "release" / f"{APP_NAME}.exe",
        pake_dir / "src-tauri" / "target" / "x86_64-pc-windows-msvc" / "release" / f"{APP_NAME}.exe",
    ]
    for c in candidates:
        if c.exists():
            return c
    # fallback: рекурсивный поиск
    for p in pake_dir.rglob(f"{APP_NAME}.exe"):
        if "bundle" not in str(p):
            return p
    return None


def build_pake_app(work: Path) -> Path:
    """Собирает Pake/Tauri-обёртку поверх панели и возвращает путь к .exe."""
    print("Собираю Pake-окно (нужны Node + pnpm + Rust, первый раз долго)...")
    web = subprocess.Popen(
        [str(ROOT / "codepilot_windows.exe"), "web", "--addr", ADDR],
        cwd=ROOT, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    try:
        for _ in range(120):
            try:
                urllib.request.urlopen(f"http://{ADDR}/api/projects", timeout=1)
                break
            except Exception:
                time.sleep(0.5)
        pake_dir = work / "pake"
        pake_dir.mkdir()
        run(["pnpm", "init"], cwd=pake_dir)
        run(["pnpm", "add", "pake-cli"], cwd=pake_dir)
        cmd = [
            "pnpm", "exec", "pake", f"http://{ADDR}",
            "--name", APP_NAME, "--width", "1280", "--height", "840",
        ]
        if (ROOT / "logo.png").exists():
            cmd += ["--icon", str(ROOT / "logo.png")]
        print("  $", " ".join(cmd))
        subprocess.run(cmd, cwd=pake_dir, check=True)
        exe = find_pake_exe(pake_dir)
        if exe is None:
            raise RuntimeError("Pake не произвёл .exe — см. вывод выше")
        return exe
    finally:
        web.terminate()


def build_launcher_exe(work: Path) -> Path:
    """Собирает launcher.py в CodePilot.exe через PyInstaller."""
    print("Собираю CodePilot.exe (PyInstaller)...")
    launcher = ROOT / "scripts" / "launcher.py"
    run([
        sys.executable, "-m", "PyInstaller",
        "--onefile",
        "--windowed",
        "--name", APP_NAME,
        "--distpath", str(work),
        "--workpath", str(work / "pyinstaller"),
        "--specpath", str(work),
        str(launcher),
    ], cwd=ROOT)
    exe = work / f"{APP_NAME}.exe"
    if not exe.exists():
        raise RuntimeError(f"PyInstaller не произвёл {exe}")
    return exe


def assemble_dir(work: Path) -> Path:
    """Собирает папку CodePilot-Windows со всем содержимым."""
    out = work / f"{APP_NAME}-Windows"
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)

    # Pake-окно — точка входа, переименованный лаунчер.
    pake_exe = build_pake_app(work)
    shutil.copy2(pake_exe, out / f"{APP_NAME}-web.exe")

    # Лаунчер — CodePilot.exe (точка входа).
    launcher_exe = build_launcher_exe(work)
    shutil.copy2(launcher_exe, out / f"{APP_NAME}.exe")

    # Go-бинарь и ORT.
    shutil.copy2(ROOT / "codepilot_windows.exe", out / "codepilot.exe")
    if (ROOT / "bin").exists():
        shutil.copytree(ROOT / "bin", out / "bin", dirs_exist_ok=True)

    # e5-small вшиваем (470 МБ), Laya докачивается после старта.
    e5 = ROOT / "models" / "e5-small"
    if e5.exists():
        shutil.copytree(e5, out / "models" / "e5-small", dirs_exist_ok=True)
    else:
        print("WARNING: models/e5-small не найден — скачается при первом запуске")

    # Скрипты первичной настройки.
    sdir = out / "scripts"
    sdir.mkdir(exist_ok=True)
    for name in ("setup_postgres.py", "setup_models.py"):
        shutil.copy2(ROOT / "scripts" / name, sdir / name)

    # Версия для проверки обновлений.
    (out / "version.txt").write_text(VERSION, encoding="utf-8")
    return out


def build_installer(app_dir: Path, work: Path) -> Path:
    """Собирает Inno Setup-установщик."""
    print("Собираю Inno Setup-установщик...")
    iss = work / "codepilot.iss"
    iss.write_text(f"""
[Setup]
AppName={APP_NAME}
AppVersion={VERSION}
DefaultDirName={{autopf}}\\{APP_NAME}
DefaultGroupName={APP_NAME}
OutputDir={ROOT / "installers" / "windows"}
OutputBaseFilename=CodePilot-Setup
Compression=lzma
SolidCompression=yes

[Files]
Source: "{app_dir}\\*"; DestDir: "{{app}}"; Flags: ignoreversion recursesubdirs

[Icons]
Name: "{{group}}\\{APP_NAME}"; Filename: "{{app}}\\{APP_NAME}.exe"
Name: "{{autodesktop}}\\{APP_NAME}"; Filename: "{{app}}\\{APP_NAME}.exe"

[Run]
Filename: "{{app}}\\{APP_NAME}.exe"; Description: "Запустить {APP_NAME}"; Flags: nowait postinstall skipifsilent
""", encoding="utf-8")
    out = ROOT / "installers" / "windows" / "CodePilot-Setup.exe"
    out.parent.mkdir(parents=True, exist_ok=True)
    run(["iscc", str(iss)], cwd=ROOT)
    # JSON-метаданные для CI / ручной проверки
    meta = {
        "version": VERSION,
        "download_url": f"https://github.com/{REPO}/releases/latest/download/CodePilot-Setup.exe",
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
    if sys.platform != "win32":
        print("сборка Windows-установщика возможна только на Windows")
        sys.exit(1)
    build_go_binary()
    ensure_ort_windows()
    ensure_models()
    with tempfile.TemporaryDirectory() as td:
        work = Path(td)
        app_dir = assemble_dir(work)
        setup = build_installer(app_dir, work)
    (ROOT / "codepilot_windows.exe").unlink(missing_ok=True)
    size_mb = setup.stat().st_size / 2**20
    print(f"\nГотово: {setup} ({size_mb:.0f} МБ)")
    print("Коллеге: запустить CodePilot-Setup.exe → установка → запуск из меню Пуск.")
    print("При первом запуске скачается модель Laya и настроится Postgres.")


if __name__ == "__main__":
    main()
