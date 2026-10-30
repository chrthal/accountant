// Package ocr reads a receipt photograph into a draft booking.
//
// Recognition is done by the local `tesseract` binary as a subprocess (ADR
// 0002, ADR 0011) — no cloud service, and no cgo binding that would cost the
// static binary. If the binary is absent the app still works; it simply cannot
// pre-fill.
//
// Whatever comes out is a draft the user checks and submits. Nothing here ever
// writes a booking.
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MaxImageSize caps an upload. Phone photos of receipts are well under this.
const MaxImageSize = 10 << 20 // 10 MiB

// Timeout bounds a recognition run. Tesseract on a phone photo takes a second
// or two; anything far past that is a stuck process, not a slow one.
const Timeout = 30 * time.Second

var (
	availableOnce sync.Once
	binaryPath    string
)

// Available reports whether the tesseract binary can be found. Handlers use it
// to explain the missing pre-fill rather than silently offering less.
func Available() bool { return BinaryPath() != "" }

// BinaryPath returns the resolved tesseract path, or "" if it is not installed.
func BinaryPath() string {
	availableOnce.Do(func() {
		if p, err := exec.LookPath("tesseract"); err == nil {
			binaryPath = p
		}
	})
	return binaryPath
}

// preferredLanguages is what we would like, best first. German for Austrian
// receipts, English for the ones that are partly English.
var preferredLanguages = []string{"deu", "eng"}

var (
	languagesOnce sync.Once
	languages     string
)

// Languages returns the `-l` argument to use: the preferred languages that are
// actually installed, joined with '+'.
//
// Asking for a language whose data is missing makes tesseract fail outright
// rather than degrade — and installations genuinely differ (the container ships
// German, a Homebrew install ships English). So the set is detected once rather
// than assumed.
func Languages() string {
	languagesOnce.Do(func() {
		installed := installedLanguages()
		var use []string
		for _, l := range preferredLanguages {
			if installed[l] {
				use = append(use, l)
			}
		}
		languages = strings.Join(use, "+")
	})
	return languages
}

func installedLanguages() map[string]bool {
	bin := BinaryPath()
	if bin == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "--list-langs").CombinedOutput()
	if err != nil {
		return nil
	}

	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// The first line is a "List of available languages…" header.
		if line == "" || strings.Contains(line, " ") {
			continue
		}
		set[line] = true
	}
	return set
}

// ErrUnavailable is returned when tesseract is not installed.
var ErrUnavailable = fmt.Errorf("tesseract is not installed")

// Recognise runs OCR over the image bytes and returns the recognised text.
//
// The image is written to a temporary file because tesseract reads a path;
// arguments are passed as argv, never through a shell, and the file is removed
// afterwards. The input is an untrusted photo and is treated as one.
func Recognise(ctx context.Context, image []byte, filename string) (string, error) {
	bin := BinaryPath()
	if bin == "" {
		return "", ErrUnavailable
	}
	if len(image) == 0 {
		return "", fmt.Errorf("the image is empty")
	}
	if len(image) > MaxImageSize {
		return "", fmt.Errorf("the image is larger than %d MiB", MaxImageSize>>20)
	}

	dir, err := os.MkdirTemp("", "bookkeeper-ocr-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	// The extension is the only thing taken from the uploaded name, and only
	// after being stripped of any path.
	ext := strings.ToLower(filepath.Ext(filepath.Base(filename)))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".tif", ".tiff", ".bmp", ".heic":
	default:
		ext = ".jpg"
	}

	path := filepath.Join(dir, "receipt"+ext)
	if err := os.WriteFile(path, image, 0o600); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	text, err := run(ctx, bin, path, Languages())
	if err != nil {
		// Fall back to tesseract's own default rather than giving up: a worse
		// read still beats no read.
		if fallback, err2 := run(ctx, bin, path, ""); err2 == nil {
			return fallback, nil
		}
		return "", err
	}
	return text, nil
}

type singleThreadKey struct{}

// SingleThreaded marks ctx for runs side by side: tesseract then uses one
// thread. Its own threads only help when it runs alone, and several runs
// that each start threads on every core are slower than one thread each.
func SingleThreaded(ctx context.Context) context.Context {
	return context.WithValue(ctx, singleThreadKey{}, true)
}

func run(ctx context.Context, bin, path, languages string) (string, error) {
	args := []string{path, "stdout"}
	if languages != "" {
		args = append(args, "-l", languages)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if ctx.Value(singleThreadKey{}) != nil {
		cmd.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")
	}

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("tesseract: %s", msg)
	}
	return stdout.String(), nil
}
