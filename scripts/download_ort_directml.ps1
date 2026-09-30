# Скачивание ONNX Runtime с DirectML EP для Windows.
# Кладёт onnxruntime.dll + shared provider + DirectML.dll в bin/.
param(
    [string]$ORTVersion = "1.23.0",
    [string]$DMLVersion = "1.15.2",
    [string]$OutDir = "$PSScriptRoot\..\bin"
)

$ErrorActionPreference = "Stop"

$ortUrl = "https://www.nuget.org/api/v2/package/Microsoft.ML.OnnxRuntime.DirectML/${ORTVersion}"
$dmlUrl = "https://www.nuget.org/api/v2/package/Microsoft.AI.DirectML/${DMLVersion}"

$tmpDir = "$env:TEMP\codepilot-ort-directml-$([Guid]::NewGuid())"
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

function Expand-Nupkg($url, $outSubdir) {
    $zip = "$tmpDir\$outSubdir.zip"
    $dest = "$tmpDir\$outSubdir"
    Write-Host "Downloading $url ..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing
    Expand-Archive -Path $zip -DestinationPath $dest -Force
    return $dest
}

$ortDir = Expand-Nupkg $ortUrl "ort"
$dmlDir = Expand-Nupkg $dmlUrl "dml"

$files = @(
    "$ortDir\runtimes\win-x64\native\onnxruntime.dll",
    "$ortDir\runtimes\win-x64\native\onnxruntime_providers_shared.dll",
    "$dmlDir\bin\x64-win\DirectML.dll"
)

foreach ($f in $files) {
    if (Test-Path $f) {
        Copy-Item -Path $f -Destination $OutDir -Force
        Write-Host "Copied $(Split-Path $f -Leaf) -> $OutDir" -ForegroundColor Green
    } else {
        Write-Error "Expected file not found: $f"
    }
}

Remove-Item -Recurse -Path $tmpDir -Force -ErrorAction SilentlyContinue

Write-Host "Done. DLLs are in $OutDir" -ForegroundColor Green
