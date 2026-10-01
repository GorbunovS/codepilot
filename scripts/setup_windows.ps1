# setup_windows.ps1 — развёртывание CodePilot на Windows без Docker.
# Требования: Go, PowerShell 5.1+, Visual Studio Build Tools (для сборки pgvector).
# Одна команда: .\scripts\setup_windows.ps1
param(
    [string]$PgVersion = "17.2-1",
    [string]$PgVectorVersion = "0.8.0",
    [int]$PgPort = 5432,
    [string]$PgPassword = "codepilot",
    [string]$BaseDir = "$env:USERPROFILE\.codepilot"
)

$ErrorActionPreference = "Stop"

$pgDir = "$BaseDir\postgres"
$dataDir = "$BaseDir\postgres-data"
$pgVectorSrc = "$BaseDir\pgvector-$PgVectorVersion"
$pgVectorZip = "$BaseDir\pgvector.zip"
$pgZip = "$BaseDir\postgresql.zip"
$logFile = "$dataDir\postmaster.log"

function Test-Command($cmd) {
    return [bool](Get-Command $cmd -ErrorAction SilentlyContinue)
}

function Wait-ForPostgres($maxSeconds = 60) {
    Write-Host "Ожидание запуска Postgres на порту $PgPort..."
    for ($i = 0; $i -lt $maxSeconds; $i++) {
        try {
            $null = & "$pgDir\bin\psql.exe" -U postgres -p $PgPort -c "SELECT 1;" 2>&1
            Write-Host "Postgres готов."
            return
        } catch {
            Start-Sleep -Seconds 1
        }
    }
    throw "Postgres не запустился за $maxSeconds секунд. См. $logFile"
}

# 1. Go
if (-not (Test-Command go)) {
    throw "Go не найден. Установите Go: https://go.dev/dl/"
}
Write-Host "Go найден: $(go version)" -ForegroundColor Green

New-Item -ItemType Directory -Force -Path $BaseDir | Out-Null

# 2. PostgreSQL binaries
if (-not (Test-Path "$pgDir\bin\postgres.exe")) {
    Write-Host "Скачиваю PostgreSQL $PgVersion binaries..." -ForegroundColor Cyan
    $url = "https://get.enterprisedb.com/postgresql/postgresql-${PgVersion}-windows-x64-binaries.zip"
    Invoke-WebRequest -Uri $url -OutFile $pgZip -UseBasicParsing
    Expand-Archive -Path $pgZip -DestinationPath $BaseDir -Force
    if (Test-Path "$BaseDir\pgsql") {
        Move-Item "$BaseDir\pgsql" $pgDir -Force
    }
    Remove-Item $pgZip -Force -ErrorAction SilentlyContinue
    Write-Host "PostgreSQL распакован в $pgDir" -ForegroundColor Green
}

# 3. Инициализация кластера
if (-not (Test-Path "$dataDir\PG_VERSION")) {
    Write-Host "Инициализация data directory..."
    & "$pgDir\bin\initdb.exe" -D $dataDir --auth=trust --username=postgres | Out-Host
    Add-Content -Path "$dataDir\postgresql.conf" -Value "`nlisten_addresses = 'localhost'`nport = $PgPort`n"
}

# 4. Запуск Postgres
$pgRunning = $false
try {
    $null = & "$pgDir\bin\psql.exe" -U postgres -p $PgPort -c "SELECT 1;" 2>&1
    $pgRunning = $true
} catch {}

if (-not $pgRunning) {
    Write-Host "Запуск Postgres..."
    & "$pgDir\bin\pg_ctl.exe" start -D $dataDir -l $logFile -o "-p $PgPort" | Out-Host
}

Wait-ForPostgres

# 5. pgvector
$vectorInstalled = Test-Path "$pgDir\lib\vector.dll"
if (-not $vectorInstalled) {
    Write-Host "pgvector не найден. Скачиваю исходники v$PgVectorVersion..." -ForegroundColor Cyan
    if (-not (Test-Command nmake) -or -not (Test-Command cl)) {
        throw "Для сборки pgvector нужны Visual Studio Build Tools (nmake + cl).`nУстановите: https://visualstudio.microsoft.com/visual-cpp-build-tools/ и запустите скрипт из 'x64 Native Tools Command Prompt'."
    }

    Invoke-WebRequest -Uri "https://github.com/pgvector/pgvector/archive/refs/tags/v${PgVectorVersion}.zip" -OutFile $pgVectorZip -UseBasicParsing
    Expand-Archive -Path $pgVectorZip -DestinationPath $BaseDir -Force
    Remove-Item $pgVectorZip -Force -ErrorAction SilentlyContinue

    Write-Host "Сборка pgvector..." -ForegroundColor Cyan
    Push-Location $pgVectorSrc
    $env:PGROOT = $pgDir
    $env:PATH = "$pgDir\bin;$env:PATH"
    nmake /f Makefile.win | Out-Host
    nmake /f Makefile.win install | Out-Host
    Pop-Location
    Write-Host "pgvector установлен." -ForegroundColor Green
}

# 6. Создание БД и расширения
$dbExists = & "$pgDir\bin\psql.exe" -U postgres -p $PgPort -tc "SELECT 1 FROM pg_database WHERE datname = 'codepilot';" 2>&1
if ($dbExists -notmatch "1") {
    Write-Host "Создание БД codepilot..."
    & "$pgDir\bin\createdb.exe" -U postgres -p $PgPort codepilot | Out-Host
}
& "$pgDir\bin\psql.exe" -U postgres -p $PgPort -d codepilot -c "CREATE EXTENSION IF NOT EXISTS vector;" | Out-Host

# 7. ONNX Runtime DirectML
if (-not (Test-Path "$PSScriptRoot\..\bin\onnxruntime.dll")) {
    Write-Host "Скачивание ONNX Runtime + DirectML..." -ForegroundColor Cyan
    & "$PSScriptRoot\download_ort_directml.ps1" | Out-Host
}

# 8. Сборка codepilot
Write-Host "Сборка codepilot.exe..." -ForegroundColor Cyan
$repoRoot = Resolve-Path "$PSScriptRoot\.."
Push-Location $repoRoot
go build -o codepilot.exe ./cmd/codepilot
Pop-Location
Write-Host "codepilot.exe собран." -ForegroundColor Green

# 9. Запуск панели
Write-Host "Запуск CodePilot web..." -ForegroundColor Green
$env:CODEPILOT_STORE = "pg"
$env:CODEPILOT_PG_DSN = "postgres://postgres@localhost:${PgPort}/codepilot?sslmode=disable"
$env:CODEPILOT_ONNXRUNTIME_DLL = "$repoRoot\bin\onnxruntime.dll"

& "$repoRoot\codepilot.exe" web --addr 127.0.0.1:8080

# По выходу останавливаем Postgres
& "$pgDir\bin\pg_ctl.exe" stop -D $dataDir -m fast | Out-Null
