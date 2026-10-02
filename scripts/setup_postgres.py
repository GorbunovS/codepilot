#!/usr/bin/env python3
"""Установка/запуск локального PostgreSQL + pgvector для codepilot.

Windows: скачивает бинарники Postgres и pgvector, initdb, старт сервера.
macOS: Docker (pgvector/pgvector:pg17) → brew (postgresql@17 + pgvector) →
       уже запущенный Postgres на 5432 используется как есть.

Запуск:
    python scripts/setup_postgres.py

После успеха выводит DSN, который можно использовать:
    codepilot web --store pg --pg-dsn <DSN>
"""
from __future__ import annotations

import getpass
import json
import os
import platform
import shutil
import subprocess
import sys
import tempfile
import urllib.request
import zipfile
from pathlib import Path

PG_VERSION = "17.6-1"
PGVECTOR_VERSION = "0.8.6"
PG_MAJOR = "17"
PORT = "5432"
DB_NAME = "codepilot"
DB_USER = "codepilot"
DB_PASS = "codepilot"


def home_dir() -> Path:
    return Path.home() / ".codepilot"


def pg_dir() -> Path:
    return home_dir() / "pgsql"


def data_dir() -> Path:
    return home_dir() / "pgdata"


def download(url: str, dest: Path):
    print(f"скачиваю {url} -> {dest}")
    dest.parent.mkdir(parents=True, exist_ok=True)
    urllib.request.urlretrieve(url, dest)


def pg_ready() -> bool:
    """Проверяет, слушает ли Postgres на 127.0.0.1:5432 (только TCP-connect)."""
    import socket
    try:
        with socket.create_connection(("127.0.0.1", int(PORT)), timeout=1):
            return True
    except OSError:
        return False


def unzip(archive: Path, dest: Path):
    print(f"распаковываю {archive} -> {dest}")
    dest.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(archive, "r") as z:
        z.extractall(dest)


def pg_bin(name: str) -> Path:
    suffix = ".exe" if platform.system() == "Windows" else ""
    return pg_dir() / "bin" / f"{name}{suffix}"


def run(cmd: list[str | Path], **kwargs):
    print("  $", " ".join(str(c) for c in cmd))
    subprocess.run([str(c) for c in cmd], check=True, **kwargs)


def setup_windows():
    system = platform.system()
    if system != "Windows":
        print(f"пока Windows-only; detected {system}")
        sys.exit(1)

    home_dir().mkdir(parents=True, exist_ok=True)
    tmp = Path(tempfile.gettempdir())

    # 1. PostgreSQL binaries
    pg_zip = tmp / f"postgresql-{PG_VERSION}-windows-x64-binaries.zip"
    if not pg_dir().exists():
        if not pg_zip.exists():
            url = f"https://get.enterprisedb.com/postgresql/postgresql-{PG_VERSION}-windows-x64-binaries.zip"
            download(url, pg_zip)
        # В архиве папка pgsql; распакуем во временную, потом перенесём
        extract_tmp = tmp / "codepilot_pg_extract"
        unzip(pg_zip, extract_tmp)
        src = extract_tmp / "pgsql"
        if not src.exists():
            raise RuntimeError(f"после распаковки не найдена {src}")
        shutil.move(str(src), str(pg_dir()))

    # 2. pgvector extension
    vector_zip = tmp / f"vector.v{PGVECTOR_VERSION}-pg{PG_MAJOR}.zip"
    vector_url = (
        f"https://github.com/andreiramani/pgvector_pgsql_windows/"
        f"releases/download/{PGVECTOR_VERSION}_{PG_MAJOR}/"
        f"vector.v{PGVECTOR_VERSION}-pg{PG_MAJOR}.zip"
    )
    if not (pg_dir() / "lib" / "vector.dll").exists():
        if not vector_zip.exists():
            download(vector_url, vector_zip)
        vector_extract = tmp / "codepilot_vector_extract"
        unzip(vector_zip, vector_extract)
        # Копируем файлы расширения в дерево Postgres
        for sub in ["lib", "share/extension"]:
            src = vector_extract / sub
            if src.exists():
                dst = pg_dir() / sub
                dst.mkdir(parents=True, exist_ok=True)
                for f in src.iterdir():
                    if f.is_file():
                        shutil.copy2(f, dst / f.name)
        # include не нужен для runtime

    # 3. Инициализация data dir
    if not (data_dir() / "PG_VERSION").exists():
        data_dir().mkdir(parents=True, exist_ok=True)
        run([pg_bin("initdb"), "-D", data_dir(), "--auth", "trust", "--encoding", "UTF8"])

    # 4. Запуск сервера
    status = subprocess.run(
        [str(pg_bin("pg_ctl")), "status", "-D", str(data_dir())],
        capture_output=True,
    )
    if status.returncode != 0:
        run([pg_bin("pg_ctl"), "-D", data_dir(), "-l", home_dir() / "pg.log", "start"])

    # 5. Создание пользователя и БД
    env = os.environ.copy()
    env["PGPASSWORD"] = DB_PASS
    env["PATH"] = str(pg_bin("psql").parent) + os.pathsep + env.get("PATH", "")

    # В Windows-бинарниках от enterprisedb суперпользователь = текущий
    # Windows-пользователь (не postgres), поэтому подключаемся от его имени.
    pg_superuser = getpass.getuser()

    def psql(args, db="postgres"):
        subprocess.run(
            [str(pg_bin("psql")), "-h", "127.0.0.1", "-p", PORT, "-U", pg_superuser, "-d", db, "-c", args],
            check=True,
            env=env,
        )

    try:
        psql(f"CREATE USER {DB_USER} WITH PASSWORD '{DB_PASS}';", db="postgres")
    except subprocess.CalledProcessError:
        print(f"пользователь {DB_USER} уже существует, обновляю пароль")
        psql(f"ALTER USER {DB_USER} WITH PASSWORD '{DB_PASS}';", db="postgres")

    try:
        psql(f"CREATE DATABASE {DB_NAME} OWNER {DB_USER};", db="postgres")
    except subprocess.CalledProcessError:
        print(f"база {DB_NAME} уже существует")

    psql("CREATE EXTENSION IF NOT EXISTS vector;", db=DB_NAME)

    dsn = f"postgres://{DB_USER}:{DB_PASS}@127.0.0.1:{PORT}/{DB_NAME}?sslmode=disable"
    print(f"\nготово: {dsn}")
    save_config(dsn)


def save_config(dsn: str):
    config = home_dir() / "config.json"
    cfg = {}
    if config.exists():
        cfg = json.loads(config.read_text(encoding="utf-8"))
    cfg["pg_dsn"] = dsn
    cfg["store"] = "pg"
    config.write_text(json.dumps(cfg, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"сохранено в {config}")


def wait_pg(timeout: int = 60):
    for _ in range(timeout * 2):
        if pg_ready():
            return True
        import time
        time.sleep(0.5)
    return False


def setup_macos_docker() -> bool:
    """Postgres в Docker: образ pgvector/pgvector:pg17, контейнер codepilot-pg."""
    if not shutil.which("docker"):
        return False
    r = subprocess.run(["docker", "info"], capture_output=True)
    if r.returncode != 0:
        print("docker установлен, но демон не запущен (открой Docker Desktop)")
        return False
    name = "codepilot-pg"
    r = subprocess.run(["docker", "inspect", name], capture_output=True)
    if r.returncode == 0:
        run(["docker", "start", name])
    else:
        run([
            "docker", "run", "-d", "--name", name,
            "-e", f"POSTGRES_USER={DB_USER}",
            "-e", f"POSTGRES_PASSWORD={DB_PASS}",
            "-e", f"POSTGRES_DB={DB_NAME}",
            "-p", f"{PORT}:5432",
            "-v", f"{home_dir() / 'pgdata-docker'}:/var/lib/postgresql/data",
            "--restart", "unless-stopped",
            f"pgvector/pgvector:pg{PG_MAJOR}",
        ])
    print("жду готовности Postgres...")
    if not wait_pg():
        raise RuntimeError("Postgres в Docker не поднялся за 60 с")
    # CREATE EXTENSION — от суперпользователя контейнера
    r = subprocess.run(
        ["docker", "exec", name, "psql", "-U", DB_USER, "-d", DB_NAME,
         "-c", "CREATE EXTENSION IF NOT EXISTS vector;"],
        capture_output=True, text=True,
    )
    if r.returncode != 0 and "permission" in r.stderr.lower():
        run(["docker", "exec", name, "psql", "-U", DB_USER, "-d", "postgres",
             "-c", f"ALTER USER {DB_USER} WITH SUPERUSER;"])
        run(["docker", "exec", name, "psql", "-U", DB_USER, "-d", DB_NAME,
             "-c", "CREATE EXTENSION IF NOT EXISTS vector;"])
    elif r.returncode != 0:
        print(r.stderr)
        raise RuntimeError("CREATE EXTENSION vector не удался")
    return True


def setup_macos_brew() -> bool:
    """Postgres через Homebrew: postgresql@17 + pgvector."""
    brew = shutil.which("brew")
    if not brew:
        return False
    run([brew, "install", f"postgresql@{PG_MAJOR}", "pgvector"])
    run([brew, "services", "start", f"postgresql@{PG_MAJOR}"])
    print("жду готовности Postgres...")
    if not wait_pg():
        raise RuntimeError("Postgres (brew) не поднялся за 60 с")
    prefix = subprocess.run(
        [brew, "--prefix", f"postgresql@{PG_MAJOR}"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()
    psql = str(Path(prefix) / "bin" / "psql")
    env = os.environ.copy()
    env["PGPASSWORD"] = DB_PASS
    try:
        subprocess.run([psql, "-d", "postgres", "-c",
                        f"CREATE USER {DB_USER} WITH PASSWORD '{DB_PASS}' SUPERUSER;"],
                       check=True, env=env, capture_output=True)
    except subprocess.CalledProcessError:
        print(f"пользователь {DB_USER} уже существует, обновляю пароль")
        subprocess.run([psql, "-d", "postgres", "-c",
                        f"ALTER USER {DB_USER} WITH PASSWORD '{DB_PASS}' SUPERUSER;"],
                       check=True, env=env)
    try:
        subprocess.run([psql, "-d", "postgres", "-c",
                        f"CREATE DATABASE {DB_NAME} OWNER {DB_USER};"],
                       check=True, env=env, capture_output=True)
    except subprocess.CalledProcessError:
        print(f"база {DB_NAME} уже существует")
    run([psql, "-d", DB_NAME, "-c", "CREATE EXTENSION IF NOT EXISTS vector;"], env=env)
    return True


def setup_macos():
    if pg_ready():
        print("Postgres уже слушает 5432 — проверяю pgvector и БД...")
        # Пробуем docker-контейнер, если он наш; иначе считаем внешний PG готовым
        if shutil.which("docker") and subprocess.run(
            ["docker", "inspect", "codepilot-pg"], capture_output=True
        ).returncode == 0:
            subprocess.run(
                ["docker", "exec", "codepilot-pg", "psql", "-U", DB_USER, "-d", DB_NAME,
                 "-c", "CREATE EXTENSION IF NOT EXISTS vector;"],
                capture_output=True,
            )
    elif not setup_macos_docker():
        print("Docker недоступен — ставлю Postgres через Homebrew...")
        if not setup_macos_brew():
            print("ERROR: нет ни Docker, ни Homebrew. Установи Docker Desktop:")
            print("  https://www.docker.com/products/docker-desktop/")
            sys.exit(1)
    dsn = f"postgres://{DB_USER}:{DB_PASS}@127.0.0.1:{PORT}/{DB_NAME}?sslmode=disable"
    print(f"\nготово: {dsn}")
    save_config(dsn)


if __name__ == "__main__":
    if platform.system() == "Windows":
        setup_windows()
    elif platform.system() == "Darwin":
        setup_macos()
    else:
        print(f"неподдерживаемая ОС: {platform.system()} (Windows/macOS)")
        sys.exit(1)
