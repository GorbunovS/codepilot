#!/bin/sh
# Скачивание onnxruntime для macOS (universal2: Intel + Apple Silicon).
set -eu

VERSION="${1:-1.23.1}"
ARCHIVE="onnxruntime-osx-universal2-${VERSION}.tgz"
URL="https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/${ARCHIVE}"

echo "скачиваю ${URL}..."
mkdir -p bin
rm -f /tmp/${ARCHIVE}
curl -fL --retry 3 -o "/tmp/${ARCHIVE}" "${URL}"

echo "распаковываю..."
tar -xzf "/tmp/${ARCHIVE}" -C /tmp

LIB_DIR="/tmp/onnxruntime-osx-universal2-${VERSION}/lib"
cp -P "${LIB_DIR}"/libonnxruntime.*.dylib bin/
ln -sf "libonnxruntime.${VERSION}.dylib" bin/libonnxruntime.dylib

rm -f /tmp/${ARCHIVE}
echo "готово: bin/libonnxruntime.dylib"
