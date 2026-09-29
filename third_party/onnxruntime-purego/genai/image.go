package genai

import (
	"fmt"

	"github.com/shota3506/onnxruntime-purego/genai/internal/api"
)

// Images represents loaded image data for processing.
type Images struct {
	ptr     api.OgaImages
	runtime *Runtime
}

// LoadImage loads a single image file from the specified path.
func (r *Runtime) LoadImage(imagePath string) (*Images, error) {
	pathBytes := stringToBytes(imagePath)

	var imagesPtr api.OgaImages
	result := r.funcs.LoadImage(&pathBytes[0], &imagesPtr)
	if err := resultError(r.funcs, result); err != nil {
		return nil, fmt.Errorf("failed to load image: %w", err)
	}

	return &Images{
		ptr:     imagesPtr,
		runtime: r,
	}, nil
}

// LoadImages loads multiple image files from the specified paths.
func (r *Runtime) LoadImages(imagePaths []string) (*Images, error) {
	if len(imagePaths) == 0 {
		return nil, fmt.Errorf("no image paths provided")
	}

	pathsArray, err := r.newStringArray(imagePaths)
	if err != nil {
		return nil, fmt.Errorf("failed to create string array: %w", err)
	}
	defer pathsArray.Close()

	var imagesPtr api.OgaImages
	result := r.funcs.LoadImages(pathsArray.ptr, &imagesPtr)
	if err := resultError(r.funcs, result); err != nil {
		return nil, fmt.Errorf("failed to load images: %w", err)
	}

	return &Images{
		ptr:     imagesPtr,
		runtime: r,
	}, nil
}

// Close releases resources associated with the images.
func (i *Images) Close() {
	if i.ptr != 0 {
		i.runtime.funcs.DestroyImages(i.ptr)
		i.ptr = 0
	}
}
