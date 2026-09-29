package genai

import (
	"fmt"

	"github.com/shota3506/onnxruntime-purego/genai/internal/api"
)

// Audios represents loaded audio data for processing.
type Audios struct {
	ptr     api.OgaAudios
	runtime *Runtime
}

// LoadAudio loads a single audio file from the specified path.
func (r *Runtime) LoadAudio(audioPath string) (*Audios, error) {
	pathBytes := stringToBytes(audioPath)

	var audiosPtr api.OgaAudios
	result := r.funcs.LoadAudio(&pathBytes[0], &audiosPtr)
	if err := resultError(r.funcs, result); err != nil {
		return nil, fmt.Errorf("failed to load audio: %w", err)
	}

	return &Audios{
		ptr:     audiosPtr,
		runtime: r,
	}, nil
}

// LoadAudios loads multiple audio files from the specified paths.
func (r *Runtime) LoadAudios(audioPaths []string) (*Audios, error) {
	if len(audioPaths) == 0 {
		return nil, fmt.Errorf("no audio paths provided")
	}

	pathsArray, err := r.newStringArray(audioPaths)
	if err != nil {
		return nil, fmt.Errorf("failed to create string array: %w", err)
	}
	defer pathsArray.Close()

	var audiosPtr api.OgaAudios
	result := r.funcs.LoadAudios(pathsArray.ptr, &audiosPtr)
	if err := resultError(r.funcs, result); err != nil {
		return nil, fmt.Errorf("failed to load audios: %w", err)
	}

	return &Audios{
		ptr:     audiosPtr,
		runtime: r,
	}, nil
}

// Close releases resources associated with the audios.
func (a *Audios) Close() {
	if a.ptr != 0 {
		a.runtime.funcs.DestroyAudios(a.ptr)
		a.ptr = 0
	}
}
