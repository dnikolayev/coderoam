package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	model := flag.String("model", defaultModelPath(), "whisper.cpp model path")
	whisperBin := flag.String("whisper-cli", "whisper-cli", "whisper.cpp CLI binary")
	ffmpegBin := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	language := flag.String("language", "en", "spoken language")
	timeout := flag.Duration("timeout", 120*time.Second, "transcription timeout")
	flag.Parse()
	if flag.NArg() != 1 {
		exitf("usage: coderoam-transcribe [flags] <audio-path>")
	}
	transcript, err := transcribe(flag.Arg(0), *model, *whisperBin, *ffmpegBin, *language, *timeout)
	if err != nil {
		exitf("%v", err)
	}
	fmt.Println(transcript)
}

func transcribe(inputPath, modelPath, whisperBin, ffmpegBin, language string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(inputPath) == "" {
		return "", fmt.Errorf("audio path is required")
	}
	if strings.TrimSpace(modelPath) == "" {
		return "", fmt.Errorf("model path is required")
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "coderoam-transcribe-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	wavPath := filepath.Join(tmpDir, "audio.wav")
	if err := run(ctx, ffmpegBin, []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", inputPath,
		"-ar", "16000", "-ac", "1",
		wavPath,
	}); err != nil {
		return "", fmt.Errorf("ffmpeg conversion failed: %w", err)
	}

	outPrefix := filepath.Join(tmpDir, "transcript")
	if err := run(ctx, whisperBin, []string{
		"-m", modelPath,
		"-f", wavPath,
		"-l", language,
		"-nt", "-np",
		"-otxt", "-of", outPrefix,
	}); err != nil {
		return "", fmt.Errorf("whisper transcription failed: %w", err)
	}
	raw, err := os.ReadFile(outPrefix + ".txt")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func run(ctx context.Context, name string, args []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out")
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, detail)
	}
	return nil
}

func defaultModelPath() string {
	if value := strings.TrimSpace(os.Getenv("CODEROAM_WHISPER_MODEL")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "whisper.cpp", "ggml-base.en.bin")
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
