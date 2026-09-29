package tests

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// TestCase represents a single ONNX test case.
type TestCase struct {
	Name      string
	ModelPath string
	DataSets  []TestDataSet
}

// TestDataSet represents a set of input/output test data.
type TestDataSet struct {
	ID      int
	Inputs  []TensorData
	Outputs []TensorData
}

// TensorData represents a single tensor (input or output).
type TensorData struct {
	Name string
	Path string
}

// LoadTestCases loads all test cases from a directory.
func LoadTestCases(testDataDir string) ([]TestCase, error) {
	entries, err := os.ReadDir(testDataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read test data directory: %w", err)
	}

	var testCases []TestCase
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		testCasePath := filepath.Join(testDataDir, entry.Name())
		modelPath := filepath.Join(testCasePath, "model.onnx")

		if _, err := os.Stat(modelPath); os.IsNotExist(err) {
			continue
		}

		testCase := TestCase{
			Name:      entry.Name(),
			ModelPath: modelPath,
		}

		dataSets, err := loadTestDataSets(testCasePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load data sets for %s: %w", entry.Name(), err)
		}
		testCase.DataSets = dataSets

		testCases = append(testCases, testCase)
	}

	return testCases, nil
}

// loadTestDataSets loads all test_data_set_* directories for a test case
func loadTestDataSets(testCaseDir string) ([]TestDataSet, error) {
	entries, err := os.ReadDir(testCaseDir)
	if err != nil {
		return nil, err
	}

	var dataSets []TestDataSet
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Look for test_data_set_N directories
		if !strings.HasPrefix(entry.Name(), "test_data_set_") {
			continue
		}

		var dataSetID int
		_, err := fmt.Sscanf(entry.Name(), "test_data_set_%d", &dataSetID)
		if err != nil {
			continue
		}

		dataSetPath := filepath.Join(testCaseDir, entry.Name())

		// Load inputs and outputs
		inputs, err := loadTensors(dataSetPath, "input")
		if err != nil {
			return nil, fmt.Errorf("failed to load inputs: %w", err)
		}

		outputs, err := loadTensors(dataSetPath, "output")
		if err != nil {
			return nil, fmt.Errorf("failed to load outputs: %w", err)
		}

		dataSet := TestDataSet{
			ID:      dataSetID,
			Inputs:  inputs,
			Outputs: outputs,
		}

		dataSets = append(dataSets, dataSet)
	}

	slices.SortFunc(dataSets, func(a, b TestDataSet) int {
		return cmp.Compare(a.ID, b.ID)
	})

	return dataSets, nil
}

// loadTensors loads all tensor files matching a prefix (input_*.pb or output_*.pb)
func loadTensors(dataSetPath, prefix string) ([]TensorData, error) {
	entries, err := os.ReadDir(dataSetPath)
	if err != nil {
		return nil, err
	}

	var tensors []TensorData
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Look for prefix_N.pb files
		if !strings.HasPrefix(entry.Name(), prefix+"_") || !strings.HasSuffix(entry.Name(), ".pb") {
			continue
		}

		tensorPath := filepath.Join(dataSetPath, entry.Name())
		tensors = append(tensors, TensorData{
			Name: entry.Name(),
			Path: tensorPath,
		})
	}

	slices.SortFunc(tensors, func(a, b TensorData) int {
		return strings.Compare(a.Name, b.Name)
	})

	return tensors, nil
}
