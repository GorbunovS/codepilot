# Скачивание ONNX Runtime с CUDA EP для Windows (NVIDIA GPU).
# Кладёт onnxruntime.dll + shared + cuda provider в bin/.
# ВНИМАНИЕ: для работы CUDA EP требуются системные библиотеки CUDA Toolkit 11.8 + cuDNN 8.x
# (cudart64_110.dll, cublas64_11.dll, cudnn64_8.dll и др.) в PATH.
param(
    [string]$Version = "1.23.1",
    [string]$OutDir = "$PSScriptRoot\..\bin"
)

$ErrorActionPreference = "Stop"

$url = "https://github.com/microsoft/onnxruntime/releases/download/v${Version}/onnxruntime-win-x64-gpu-${Version}.zip"
$tmpZip = "$env:TEMP\onnxruntime-win-x64-gpu-${Version}.zip"
$tmpDir = "$env:TEMP\onnxruntime-win-x64-gpu-${Version}"

Write-Host "Downloading $url ..." -ForegroundColor Cyan
Invoke-WebRequest -Uri $url -OutFile $tmpZip -UseBasicParsing

Write-Host "Extracting ..." -ForegroundColor Cyan
if (Test-Path $tmpDir) { Remove-Item -Recurse -Force $tmpDir }
Expand-Archive -Path $tmpZip -DestinationPath $tmpDir -Force

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$files = @(
    "$tmpDir\onnxruntime-win-x64-gpu-${Version}\lib\onnxruntime.dll",
    "$tmpDir\onnxruntime-win-x64-gpu-${Version}\lib\onnxruntime_providers_shared.dll",
    "$tmpDir\onnxruntime-win-x64-gpu-${Version}\lib\onnxruntime_providers_cuda.dll"
)

foreach ($f in $files) {
    if (Test-Path $f) {
        Copy-Item -Path $f -Destination $OutDir -Force
        Write-Host "Copied $(Split-Path $f -Leaf) -> $OutDir" -ForegroundColor Green
    } else {
        Write-Error "Expected file not found: $f"
    }
}

Remove-Item -Path $tmpZip -Force -ErrorAction SilentlyContinue
Remove-Item -Recurse -Path $tmpDir -Force -ErrorAction SilentlyContinue

Write-Host "Done. DLLs are in $OutDir" -ForegroundColor Green
Write-Host "Make sure CUDA Toolkit 11.8 + cuDNN 8.x libraries are in PATH." -ForegroundColor Yellow
