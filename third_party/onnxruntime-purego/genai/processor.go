package genai

import (
	"fmt"

	"github.com/shota3506/onnxruntime-purego/genai/internal/api"
	"github.com/shota3506/onnxruntime-purego/internal/cstrings"
)

// MultiModalProcessor handles processing of multi-modal inputs (text, images, audio).
type MultiModalProcessor struct {
	ptr     api.OgaMultiModalProcessor
	runtime *Runtime
}

// NewMultiModalProcessor creates a multi-modal processor for the model.
func (m *Model) NewMultiModalProcessor() (*MultiModalProcessor, error) {
	var processorPtr api.OgaMultiModalProcessor
	result := m.runtime.funcs.CreateMultiModalProcessor(m.ptr, &processorPtr)
	if err := resultError(m.runtime.funcs, result); err != nil {
		return nil, fmt.Errorf("failed to create multi-modal processor: %w", err)
	}

	return &MultiModalProcessor{
		ptr:     processorPtr,
		runtime: m.runtime,
	}, nil
}

// Close releases resources associated with the processor.
func (p *MultiModalProcessor) Close() {
	if p.ptr != 0 {
		p.runtime.funcs.DestroyMultiModalProcessor(p.ptr)
		p.ptr = 0
	}
}

// NamedTensors represents a collection of named tensors.
type NamedTensors struct {
	ptr     api.OgaNamedTensors
	runtime *Runtime
}

// Close releases resources associated with the named tensors.
func (n *NamedTensors) Close() {
	if n.ptr != 0 {
		n.runtime.funcs.DestroyNamedTensors(n.ptr)
		n.ptr = 0
	}
}

// ProcessAudios processes audio with the given prompt.
func (p *MultiModalProcessor) ProcessAudios(prompt string, audios *Audios) (*NamedTensors, error) {
	promptBytes := stringToBytes(prompt)

	var tensorsPtr api.OgaNamedTensors
	result := p.runtime.funcs.ProcessorProcessAudios(p.ptr, &promptBytes[0], audios.ptr, &tensorsPtr)
	if err := resultError(p.runtime.funcs, result); err != nil {
		return nil, fmt.Errorf("failed to process audios: %w", err)
	}

	return &NamedTensors{
		ptr:     tensorsPtr,
		runtime: p.runtime,
	}, nil
}

// ProcessImages processes images with the given prompt.
func (p *MultiModalProcessor) ProcessImages(prompt string, images *Images) (*NamedTensors, error) {
	promptBytes := stringToBytes(prompt)

	var tensorsPtr api.OgaNamedTensors
	result := p.runtime.funcs.ProcessorProcessImages(p.ptr, &promptBytes[0], images.ptr, &tensorsPtr)
	if err := resultError(p.runtime.funcs, result); err != nil {
		return nil, fmt.Errorf("failed to process images: %w", err)
	}

	return &NamedTensors{
		ptr:     tensorsPtr,
		runtime: p.runtime,
	}, nil
}

// ProcessImagesAndAudios processes both images and audios with the given prompt.
func (p *MultiModalProcessor) ProcessImagesAndAudios(prompt string, images *Images, audios *Audios) (*NamedTensors, error) {
	promptBytes := stringToBytes(prompt)

	var tensorsPtr api.OgaNamedTensors
	result := p.runtime.funcs.ProcessorProcessImagesAndAudios(p.ptr, &promptBytes[0], images.ptr, audios.ptr, &tensorsPtr)
	if err := resultError(p.runtime.funcs, result); err != nil {
		return nil, fmt.Errorf("failed to process images and audios: %w", err)
	}

	return &NamedTensors{
		ptr:     tensorsPtr,
		runtime: p.runtime,
	}, nil
}

// Decode converts token IDs to text using the processor.
func (p *MultiModalProcessor) Decode(tokens []int32) (string, error) {
	if len(tokens) == 0 {
		return "", nil
	}

	var outStringPtr *byte
	result := p.runtime.funcs.ProcessorDecode(
		p.ptr,
		&tokens[0],
		uintptr(len(tokens)),
		&outStringPtr,
	)
	if err := resultError(p.runtime.funcs, result); err != nil {
		return "", fmt.Errorf("failed to decode tokens: %w", err)
	}

	if outStringPtr == nil {
		return "", nil
	}

	text := cstrings.CStringToString(outStringPtr)

	p.runtime.funcs.DestroyString(outStringPtr)

	return text, nil
}
