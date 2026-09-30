package main

import (
	"fmt"
	"os"

	"codepilot/internal/chunk"
	"codepilot/internal/embed"
	"codepilot/internal/laya"
)

func main() {
	device := os.Getenv("CODEPILOT_DEVICE")
	if device == "" {
		device = "directml"
	}
	providers, err := embed.ProvidersForDevice(device)
	if err != nil {
		fmt.Fprintln(os.Stderr, "providers:", err)
		os.Exit(1)
	}
	fmt.Println("device:", device, "providers:", providers)

	scorer, err := laya.Load("models/laya-multilingual", 0, providers...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "laya.Load:", err)
		os.Exit(1)
	}
	defer scorer.Close()

	score := scorer.Score("где проверяется токен доступа", chunk.Chunk{
		Content: "func ValidateToken(token string) (string, error) — проверяет токен",
	})
	fmt.Printf("ok: score=%v\n", score)
}
