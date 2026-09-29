#!/bin/bash
# Download ONNX test data
#
# This script downloads ONNX backend test data used by microsoft/onnxruntime
# Reference: https://github.com/microsoft/onnxruntime/blob/main/onnxruntime/test/onnx/README.txt
# The test data format follows the ONNX backend test specification
# Reference: https://github.com/onnx/onnx/blob/main/docs/OnnxBackendTest.md

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_DATA_DIR="${SCRIPT_DIR}/testdata"

echo "==> Downloading ONNX backend test data"
echo "    This follows the same test data format as microsoft/onnxruntime"

# Check if Python and pip are available
if ! command -v python3 &> /dev/null; then
    echo "Error: python3 is required but not installed"
    exit 1
fi

if ! command -v pip3 &> /dev/null; then
    echo "Error: pip3 is required but not installed"
    exit 1
fi

# Setup Python environment
VENV_DIR="${SCRIPT_DIR}/.venv"

# Create virtual environment if it doesn't exist
if [ ! -d "${VENV_DIR}" ]; then
    echo "==> Creating Python virtual environment..."
    python3 -m venv "${VENV_DIR}"
fi

# Activate virtual environment
echo "==> Activating virtual environment..."
source "${VENV_DIR}/bin/activate"

# Install onnx package if not already installed
echo "==> Checking for onnx package..."
if ! python3 -c "import onnx" 2>/dev/null; then
    echo "==> Installing onnx package..."
    pip install onnx
else
    echo "==> onnx package already installed"
fi

# Check if backend-test-tools is available
if ! command -v backend-test-tools &> /dev/null; then
    echo "==> backend-test-tools not found in PATH"
    echo "==> Trying to locate it..."

    # Try to find backend-test-tools in common locations
    POSSIBLE_PATHS=(
        "$HOME/.local/bin/backend-test-tools"
        "$(python3 -m site --user-base)/bin/backend-test-tools"
        "/usr/local/bin/backend-test-tools"
    )

    BACKEND_TEST_TOOLS=""
    for path in "${POSSIBLE_PATHS[@]}"; do
        if [ -f "$path" ]; then
            BACKEND_TEST_TOOLS="$path"
            echo "==> Found backend-test-tools at: $path"
            break
        fi
    done

    if [ -z "$BACKEND_TEST_TOOLS" ]; then
        echo "Error: backend-test-tools not found. Please ensure onnx is properly installed."
        echo "Try: pip3 install --user onnx"
        exit 1
    fi
else
    BACKEND_TEST_TOOLS="backend-test-tools"
fi

# Create test data directory
mkdir -p "${TEST_DATA_DIR}"

# Generate test data for selected operators
# We'll start with a small subset of critical operators to keep download size manageable
echo "==> Generating test data for selected operators..."

# Generate all backend test data to default location
echo "==> Generating ONNX backend test data (this may take a few minutes)..."
"${BACKEND_TEST_TOOLS}" generate-data

# Get the generated data location
ONNX_DATA_DIR="${VENV_DIR}/lib/python3.13/site-packages/onnx/backend/test/data/node"

# Check if data was generated
if [ ! -d "${ONNX_DATA_DIR}" ]; then
    echo "Error: Generated test data not found at ${ONNX_DATA_DIR}"
    exit 1
fi

# List of high-priority operators to include in our test suite
# Reference: https://github.com/microsoft/onnxruntime/tree/main/onnxruntime/test/testdata
PRIORITY_OPERATORS=(
    "test_add"
    "test_sub"
    "test_mul"
    "test_div"
    "test_relu"
    "test_sigmoid"
    "test_tanh"
    "test_matmul"
    "test_gemm"
    "test_conv"
    "test_batchnorm"
    "test_maxpool"
    "test_averagepool"
    "test_concat"
    "test_reshape"
    "test_transpose"
    "test_softmax"
    "test_flatten"
    "test_identity"
    "test_constant"
)

# Copy selected operator tests to our test data directory
echo "==> Selecting high-priority operator tests..."
COPIED_COUNT=0
for op_pattern in "${PRIORITY_OPERATORS[@]}"; do
    # Find and copy matching test directories
    find "${ONNX_DATA_DIR}" -type d -name "${op_pattern}*" | while read -r test_dir; do
        test_name=$(basename "$test_dir")
        dest_dir="${TEST_DATA_DIR}/${test_name}"

        if [ -d "$test_dir" ] && [ -f "$test_dir/model.onnx" ]; then
            cp -r "$test_dir" "$dest_dir"
            echo "    Copied: ${test_name}"
            COPIED_COUNT=$((COPIED_COUNT + 1))
        fi
    done
done

TOTAL_TESTS=$(find "${TEST_DATA_DIR}" -name "model.onnx" | wc -l | tr -d ' ')

echo ""
echo "==> Test data download complete!"
echo "    Location: ${TEST_DATA_DIR}"
echo "    Total test cases: ${TOTAL_TESTS}"
echo ""
echo "Test data structure follows microsoft/onnxruntime format:"
echo "  testdata/"
echo "    test_<operator>/"
echo "      model.onnx"
echo "      test_data_set_0/"
echo "        input_0.pb"
echo "        output_0.pb"
echo ""
echo "Reference: https://github.com/microsoft/onnxruntime/tree/main/onnxruntime/test"
