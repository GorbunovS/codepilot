# ONNX Runtime E2E Tests

This directory contains end-to-end tests for onnxruntime-purego using the ONNX backend test data format.

## Reference

This test suite follows the approach used by microsoft/onnxruntime:

https://github.com/onnx/onnx/blob/main/docs/OnnxBackendTest.md

## Prerequisites

1. **Python 3** with pip
3. **ONNX Runtime library**: Set `ONNXRUNTIME_LIB_PATH` environment variable

## Setup

### 1. Download Test Data

Run the download script to fetch ONNX backend test data:

```bash
cd onnxruntime/internal/tests
./download_test_data.sh
```

This will:
- Install the `onnx` Python package if needed
- Generate ONNX backend test data using `backend-test-tools`
- Extract selected high-priority operator tests
- Place test data in `testdata/` directory

## Running Tests

### Run All E2E Tests

```bash
cd onnxruntime/internal/tests
go test -v
```
