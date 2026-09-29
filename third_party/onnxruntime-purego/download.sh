#!/bin/bash
#
# ONNX Runtime Download Script
# Usage: ./download.sh [VERSION]
# Example: ./download.sh 1.23.0
#

set -e

VERSION="${1:-1.23.0}"
LIBS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/libs"
TARGET_DIR="${LIBS_DIR}/${VERSION}"

# Detect platform
detect_platform() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    local arch=$(uname -m)

    case "$os" in
        darwin)
            OS="osx"
            ;;
        linux)
            OS="linux"
            ;;
        *)
            echo "Error: Unsupported OS: $os"
            exit 1
            ;;
    esac

    case "$arch" in
        x86_64|amd64)
            ARCH="x64"
            ;;
        arm64|aarch64)
            if [ "$OS" = "linux" ]; then
                ARCH="aarch64"
            else
                ARCH="arm64"
            fi
            ;;
        *)
            echo "Error: Unsupported architecture: $arch"
            exit 1
            ;;
    esac

    echo "Detected platform: ${OS}-${ARCH}"
}

# Build download URL
build_url() {
    local base_url="https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}"
    FILENAME="onnxruntime-${OS}-${ARCH}-${VERSION}.tgz"
    DOWNLOAD_URL="${base_url}/${FILENAME}"

    echo "Download URL: ${DOWNLOAD_URL}"
}

main() {
    echo "=========================================="
    echo "ONNX Runtime Download (v${VERSION})"
    echo "=========================================="

    detect_platform
    build_url

    # Skip if already exists
    if [ -d "$TARGET_DIR" ]; then
        echo "Already downloaded: $TARGET_DIR"
        exit 0
    fi

    # Create temporary directory
    TEMP_DIR=$(mktemp -d)
    trap "rm -rf $TEMP_DIR" EXIT

    cd "$TEMP_DIR"

    # Download
    echo "Downloading..."
    if command -v curl &> /dev/null; then
        curl -L -o "$FILENAME" --progress-bar "$DOWNLOAD_URL"
    elif command -v wget &> /dev/null; then
        wget -q --show-progress "$DOWNLOAD_URL"
    else
        echo "Error: curl or wget is required"
        exit 1
    fi

    # Extract
    echo "Extracting..."
    tar xzf "$FILENAME"

    # Find and move extracted directory
    EXTRACTED_DIR=$(find . -maxdepth 1 -type d -name "onnxruntime-*" | head -n 1)
    if [ -z "$EXTRACTED_DIR" ]; then
        echo "Error: Extracted directory not found"
        exit 1
    fi

    # Move to libs directory
    mkdir -p "$LIBS_DIR"
    mv "$EXTRACTED_DIR" "$TARGET_DIR"

    echo "=========================================="
    echo "Completed: $TARGET_DIR"
    echo "=========================================="

    # Display library files
    echo ""
    echo "Library files:"
    ls -lh "$TARGET_DIR/lib/"
}

main
