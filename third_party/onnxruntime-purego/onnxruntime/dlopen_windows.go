//go:build windows

package onnxruntime

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// dlopenLibrary загружает DLL через WinAPI (purego.Dlopen на Windows нет;
// purego.RegisterLibFunc/GetProcAddress дальше работают с этим хэндлом).
func dlopenLibrary(path string) (uintptr, error) {
	h, err := windows.LoadLibrary(path)
	if err == syscall.Errno(0) {
		err = nil
	}
	return uintptr(h), err
}

// encodeModelPath — на Windows ORTCHAR_T = wchar_t: путь в UTF-16LE с NUL.
func encodeModelPath(path string) []byte {
	u16, err := windows.UTF16FromString(path)
	if err != nil {
		return append([]byte(path), 0)
	}
	buf := make([]byte, 2*len(u16))
	for i, c := range u16 {
		buf[2*i] = byte(c)
		buf[2*i+1] = byte(c >> 8)
	}
	return buf
}
