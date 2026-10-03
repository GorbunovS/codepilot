#!/usr/bin/env python3
"""Windows installer builder for codepilot (Inno Setup).

Contents:
- CodePilot.exe: Pake window (Tauri) over web panel + Go binary,
  bin/onnxruntime.dll, models/e5-small, first-run setup scripts.
  Entry point is CodePilot.exe (compiled launcher.py): starts
  `codepilot web` and opens the window. Models/Postgres setup happens after
  app start (onboarding UI).
- Inno Setup installer: CodePilot-Setup.exe.

Build machine requirements: Go, Node.js + pnpm, Rust (for Pake/Tauri),
Python + PyInstaller, Inno Setup (iscc).

Build:
    python scripts/build_windows.py

Result: installers/windows/CodePilot-Setup.exe (gitignored).
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
DEFAULT_VERSION = ""  # auto: build date YYYY.MM.DD-HHMM
ADDR = "127.0.0.1:8080"
REPO = "GorbunovS/codepilot"
ORT_VERSION = "1.23.1"


def run(cmd: list[str | Path] | str, shell: bool = False, **kwargs):
    if isinstance(cmd, str):
        print("  $", cmd)
        subprocess.run(cmd, check=True, shell=shell, **kwargs)
    else:
        print("  $", " ".join(str(c) for c in cmd))
        subprocess.run([str(c) for c in cmd], check=True, shell=shell, **kwargs)


def parse_args():
    p = argparse.ArgumentParser()
    p.add_argument("--version", default=DEFAULT_VERSION, help="build version (empty = auto YYYY.MM.DD-HHMM)")
    return p.parse_args()


def build_go_binary():
    print("Building codepilot.exe (windows/amd64)...")
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = "windows"
    env["GOARCH"] = "amd64"
    run(["go", "build", "-o", "codepilot_windows.exe", "./cmd/codepilot"], cwd=ROOT, env=env)


def ensure_ort_windows():
    dll = ROOT / "bin" / "onnxruntime.dll"
    if dll.exists():
        print("onnxruntime.dll already present")
        return
    print("Downloading ONNX Runtime for Windows...")
    tmp = Path(tempfile.gettempdir())
    zip_path = tmp / f"onnxruntime-win-x64-{ORT_VERSION}.zip"
    if not zip_path.exists():
        url = (
            f"https://github.com/microsoft/onnxruntime/releases/download/"
            f"v{ORT_VERSION}/onnxruntime-win-x64-{ORT_VERSION}.zip"
        )
        print(f"downloading {url} -> {zip_path}")
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
    print(f"copied to {ROOT / 'bin'}")


def ensure_models():
    print("Downloading models (e5-small)...")
    setup = ROOT / "scripts" / "setup_models.py"
    run([sys.executable, str(setup)], cwd=ROOT)


def find_pake_exe(pake_dir: Path) -> Path | None:
    """Finds built Pake .exe in build tree."""
    candidates = [
        pake_dir / "src-tauri" / "target" / "release" / f"{APP_NAME}.exe",
        pake_dir / "src-tauri" / "target" / "x86_64-pc-windows-msvc" / "release" / f"{APP_NAME}.exe",
    ]
    for c in candidates:
        if c.exists():
            return c
    # fallback: recursive search
    for p in pake_dir.rglob(f"{APP_NAME}.exe"):
        if "bundle" not in str(p):
            return p
    return None


def pnpm_cmd_str(*args: str | Path) -> str:
    """Returns pnpm shell command, falling back to npx pnpm if pnpm is not in PATH."""
    base = "pnpm" if shutil.which("pnpm") else "npx pnpm"
    return base + " " + " ".join(str(a) for a in args)


def build_pake_app(work: Path) -> Path:
    """Builds Pake/Tauri wrapper over the panel and returns path to .exe."""
    print("Building Pake window (Node + pnpm + Rust required, first run is slow)...")
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
        run(pnpm_cmd_str("init"), shell=True, cwd=pake_dir)
        run(pnpm_cmd_str("add", "pake-cli"), shell=True, cwd=pake_dir)
        cmd = pnpm_cmd_str("exec", "pake", f"http://{ADDR}", "--name", APP_NAME, "--width", "1280", "--height", "840")
        if (ROOT / "logo.png").exists():
            cmd += f" --icon {ROOT / 'logo.png'}"
        print("  $", cmd)
        subprocess.run(cmd, shell=True, cwd=pake_dir, check=True)
        exe = find_pake_exe(pake_dir)
        if exe is None:
            raise RuntimeError("Pake did not produce .exe — see output above")
        return exe
    finally:
        web.terminate()


def build_launcher_exe(work: Path) -> Path:
    """Builds launcher.py into CodePilot.exe via PyInstaller."""
    print("Building CodePilot.exe (PyInstaller)...")
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
        raise RuntimeError(f"PyInstaller did not produce {exe}")
    return exe


def assemble_dir(work: Path) -> Path:
    """Builds CodePilot-Windows directory with all contents."""
    out = work / f"{APP_NAME}-Windows"
    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True)

    # Pake window - entry point renamed launcher.
    pake_exe = build_pake_app(work)
    shutil.copy2(pake_exe, out / f"{APP_NAME}-web.exe")

    # Launcher - CodePilot.exe (entry point).
    launcher_exe = build_launcher_exe(work)
    shutil.copy2(launcher_exe, out / f"{APP_NAME}.exe")

    # Go binary and ORT.
    shutil.copy2(ROOT / "codepilot_windows.exe", out / "codepilot.exe")
    if (ROOT / "bin").exists():
        shutil.copytree(ROOT / "bin", out / "bin", dirs_exist_ok=True)

    # e5-small is bundled (~470 MB), Laya is downloaded after start.
    e5 = ROOT / "models" / "e5-small"
    if e5.exists():
        shutil.copytree(e5, out / "models" / "e5-small", dirs_exist_ok=True)
    else:
        print("WARNING: models/e5-small not found — will be downloaded on first run")

    # First-run setup scripts.
    sdir = out / "scripts"
    sdir.mkdir(exist_ok=True)
    for name in ("setup_postgres.py", "setup_models.py"):
        shutil.copy2(ROOT / "scripts" / name, sdir / name)

    # Version for update checks.
    (out / "version.txt").write_text(VERSION, encoding="utf-8")
    return out


def iscc_cmd(*args: str | Path) -> list[str]:
    """Returns Inno Setup compiler command, with fallback to standard install paths."""
    if shutil.which("iscc"):
        return ["iscc", *(str(a) for a in args)]
    for root in (os.environ.get("ProgramFiles(x86)"), os.environ.get("ProgramFiles")):
        if root:
            p = Path(root) / "Inno Setup 6" / "ISCC.exe"
            if p.exists():
                return [str(p), *(str(a) for a in args)]
    return ["iscc", *(str(a) for a in args)]


def build_installer(app_dir: Path, work: Path) -> Path:
    """Builds Inno Setup installer."""
    print("Building Inno Setup installer...")
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
Filename: "{{app}}\\{APP_NAME}.exe"; Description: "Run {APP_NAME}"; Flags: nowait postinstall skipifsilent
""", encoding="utf-8")
    out = ROOT / "installers" / "windows" / "CodePilot-Setup.exe"
    out.parent.mkdir(parents=True, exist_ok=True)
    run(iscc_cmd(iss), cwd=ROOT)
    # JSON metadata for CI / manual checks
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
        print(f"auto version: {VERSION}")
    if sys.platform != "win32":
        print("Windows installer build is only possible on Windows")
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
    print(f"\nDone: {setup} ({size_mb:.0f} MB)")
    print("For colleague: run CodePilot-Setup.exe → install → launch from Start menu.")
    print("Laya model will be downloaded and Postgres will be configured on first run.")


if __name__ == "__main__":
    main()
