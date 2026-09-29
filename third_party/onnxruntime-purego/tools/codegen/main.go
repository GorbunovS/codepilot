package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"go/format"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

const (
	headerURLTemplate = "https://raw.githubusercontent.com/microsoft/onnxruntime/v%s/include/onnxruntime/core/session/onnxruntime_c_api.h"
)

var (
	// Regular expressions for parsing function pointer patterns
	macroPattern   = regexp.MustCompile(`ORT_API2_STATUS\(([A-Za-z0-9_]+)`)
	releasePattern = regexp.MustCompile(`ORT_CLASS_RELEASE\(([A-Za-z0-9_]+)\)`)
	directPattern  = regexp.MustCompile(`\*\s*([A-Z][a-zA-Z0-9_]*)\)`)
)

//go:embed templates/api.go.tmpl
var apiTemplate string

type Function struct {
	Name  string
	Index int
}

type GeneratorConfig struct {
	Version     string
	Functions   []Function
	PackageName string
	HeaderURL   string
}

func main() {
	version := flag.String("version", "1.23.0", "ONNX Runtime version (e.g., 1.23.0)")
	outDir := flag.String("out", "", "Output directory (e.g., onnxruntime/internal/api/v23)")
	flag.Parse()

	if *outDir == "" {
		log.Fatal("Output directory is required (-out flag)")
	}

	// Parse version to get API version number
	parts := strings.Split(*version, ".")
	if len(parts) < 2 {
		log.Fatal("Invalid version format. Expected format: X.Y.Z")
	}
	apiVersion := parts[1] // e.g., "1.23.0" -> "23"

	// Download header file
	headerURL := fmt.Sprintf(headerURLTemplate, *version)
	log.Printf("Downloading header from %s", headerURL)
	resp, err := http.Get(headerURL)
	if err != nil {
		log.Fatalf("Failed to download header: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Fatalf("Failed to download header: HTTP %d", resp.StatusCode)
	}

	// Read and parse header file
	scanner := bufio.NewScanner(resp.Body)
	functions, err := parseOrtAPIStruct(scanner)
	if err != nil {
		log.Fatalf("Failed to parse header: %v", err)
	}

	log.Printf("Found %d functions in OrtApi struct", len(functions))

	// Prepare generator config
	config := GeneratorConfig{
		Version:     apiVersion,
		Functions:   functions,
		PackageName: "v" + apiVersion,
		HeaderURL:   headerURL,
	}

	// Create output directory
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Generate api.go (includes types, API struct, and helper functions)
	if err := executeTemplate(filepath.Join(*outDir, "api.go"), apiTemplate, config); err != nil {
		log.Fatalf("Failed to generate api.go: %v", err)
	}

	log.Println("Generated api.go")
	log.Println("Code generation completed successfully!")
	log.Println("Note: funcs.go must be created manually for typed function wrappers")
}

func parseOrtAPIStruct(scanner *bufio.Scanner) ([]Function, error) {
	var functions []Function
	inStruct := false
	index := 0

	addFunction := func(name string) {
		functions = append(functions, Function{Name: name, Index: index})
		index++
	}

	isCommentOrEmpty := func(s string) bool {
		return s == "" || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/*") ||
			strings.HasPrefix(s, "*") || strings.HasPrefix(s, "///")
	}

	for scanner.Scan() {
		line := scanner.Text()

		// Start of OrtApi struct
		if strings.Contains(line, "struct OrtApi {") {
			inStruct = true
			continue
		}

		// End of OrtApi struct
		if inStruct && strings.HasPrefix(strings.TrimSpace(line), "};") {
			break
		}

		if !inStruct {
			continue
		}

		// Skip comments and empty lines
		trimmed := strings.TrimSpace(line)
		if isCommentOrEmpty(trimmed) {
			continue
		}

		// Try macro pattern first (ORT_API2_STATUS)
		if match := macroPattern.FindStringSubmatch(line); match != nil {
			addFunction(match[1])
			continue
		}

		// Try release pattern (ORT_CLASS_RELEASE)
		if match := releasePattern.FindStringSubmatch(line); match != nil {
			addFunction("Release" + match[1])
			continue
		}

		// Try direct function pointer pattern
		if match := directPattern.FindStringSubmatch(line); match != nil {
			addFunction(match[1])
		}
	}

	return functions, scanner.Err()
}

func executeTemplate(path, tmplStr string, config GeneratorConfig) error {
	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, config); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Format the generated code
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// If formatting fails, write unformatted code for debugging
		log.Printf("Warning: failed to format code: %v", err)
		formatted = buf.Bytes()
	}

	if err := os.WriteFile(path, formatted, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}
