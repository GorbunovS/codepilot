//go:build (darwin || freebsd || linux || netbsd) && !android

package onnxruntime

import "github.com/ebitengine/purego"

func dlopenLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

// encodeModelPath — путь к модели как C-строка (unix: char*).
func encodeModelPath(path string) []byte {
	return append([]byte(path), 0)
}
