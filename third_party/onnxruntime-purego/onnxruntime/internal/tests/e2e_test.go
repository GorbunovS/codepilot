package tests

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/shota3506/onnxruntime-purego/onnxruntime"
)

func TestE2E(t *testing.T) {
	testCases, err := LoadTestCases(testDataDir)
	if err != nil {
		t.Fatalf("Failed to load test cases: %v", err)
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			env, err := testRuntime.NewEnv("test", onnxruntime.LoggingLevelWarning)
			if err != nil {
				t.Fatalf("Failed to create environment: %v", err)
			}
			defer env.Close()

			modelData, err := os.ReadFile(tc.ModelPath)
			if err != nil {
				t.Fatalf("Failed to read model: %v", err)
			}

			// Create session
			session, err := testRuntime.NewSessionFromReader(env, bytes.NewReader(modelData), nil)
			if err != nil {
				// Skip test cases with unsupported IR version
				if strings.Contains(err.Error(), "Unsupported model IR version") {
					t.Skipf("Skipping due to unsupported IR version: %v", err)
				}
				t.Fatalf("Failed to create session: %v", err)
			}
			defer session.Close()

			for _, dataSet := range tc.DataSets {
				t.Run(fmt.Sprintf("dataset_%d", dataSet.ID), func(t *testing.T) {
					runTestDataSet(t, testRuntime, session, dataSet)
				})
			}
		})
	}
}

// runTestDataSet runs inference on a single test data set and validates output
func runTestDataSet(t *testing.T, runtime *onnxruntime.Runtime, session *onnxruntime.Session, dataSet TestDataSet) {
	t.Helper()

	inputNames := session.InputNames()
	outputNames := session.OutputNames()

	// Load input tensors
	inputs := make(map[string]*onnxruntime.Value)
	defer func() {
		for _, v := range inputs {
			v.Close()
		}
	}()

	for i, inputData := range dataSet.Inputs {
		if i >= len(inputNames) {
			t.Fatalf("More input files than expected inputs")
		}

		data, shape, err := LoadTestData(inputData.Path)
		if err != nil {
			// Skip test cases with unsupported data types
			if strings.Contains(err.Error(), "unsupported data type") {
				t.Skipf("Skipping due to unsupported data type: %v", err)
			}
			t.Fatalf("Failed to load input %s: %v", inputData.Name, err)
		}

		var tensor *onnxruntime.Value
		switch v := data.(type) {
		case []float32:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		case []int64:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		case []int32:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		case []int16:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		case []int8:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		case []uint8:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		case []uint16:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		case []uint32:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		case []uint64:
			tensor, err = onnxruntime.NewTensorValue(runtime, v, shape)
		default:
			t.Fatalf("Unsupported data type: %T", data)
		}
		if err != nil {
			t.Fatalf("Failed to create tensor: %v", err)
		}

		inputs[inputNames[i]] = tensor
	}

	outputs, err := session.Run(t.Context(), inputs)
	if err != nil {
		t.Fatalf("Failed to run inference: %v", err)
	}
	defer func() {
		for _, v := range outputs {
			v.Close()
		}
	}()

	for i, outputData := range dataSet.Outputs {
		if i >= len(outputNames) {
			t.Fatalf("More output files than expected outputs")
		}

		outputName := outputNames[i]
		actualOutput, ok := outputs[outputName]
		if !ok {
			t.Fatalf("Output %s not found in inference results", outputName)
		}

		// Load expected output
		expectedData, expectedShape, err := LoadTestData(outputData.Path)
		if err != nil {
			// Skip test cases with unsupported data types
			if strings.Contains(err.Error(), "unsupported data type") {
				t.Skipf("Skipping due to unsupported data type: %v", err)
			}
			t.Fatalf("Failed to load expected output: %v", err)
		}

		switch expected := expectedData.(type) {
		case []float32:
			actual, actualShape, err := onnxruntime.GetTensorData[float32](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareFloat32Tensors(t, actual, actualShape, expected, expectedShape)

		case []int64:
			actual, actualShape, err := onnxruntime.GetTensorData[int64](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareInt64Tensors(t, actual, actualShape, expected, expectedShape)

		case []int32:
			actual, actualShape, err := onnxruntime.GetTensorData[int32](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareInt32Tensors(t, actual, actualShape, expected, expectedShape)

		case []int16:
			actual, actualShape, err := onnxruntime.GetTensorData[int16](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareInt16Tensors(t, actual, actualShape, expected, expectedShape)

		case []int8:
			actual, actualShape, err := onnxruntime.GetTensorData[int8](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareInt8Tensors(t, actual, actualShape, expected, expectedShape)

		case []uint8:
			actual, actualShape, err := onnxruntime.GetTensorData[uint8](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareUint8Tensors(t, actual, actualShape, expected, expectedShape)

		case []uint16:
			actual, actualShape, err := onnxruntime.GetTensorData[uint16](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareUint16Tensors(t, actual, actualShape, expected, expectedShape)

		case []uint32:
			actual, actualShape, err := onnxruntime.GetTensorData[uint32](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareUint32Tensors(t, actual, actualShape, expected, expectedShape)

		case []uint64:
			actual, actualShape, err := onnxruntime.GetTensorData[uint64](actualOutput)
			if err != nil {
				t.Fatalf("Failed to get actual output data: %v", err)
			}
			compareUint64Tensors(t, actual, actualShape, expected, expectedShape)

		default:
			t.Fatalf("Unsupported expected data type: %T", expected)
		}
	}
}
