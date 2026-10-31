package xray

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrNotInstalled — исполняемый файл Xray не найден.
var ErrNotInstalled = errors.New("xray не найден")

// FindBin возвращает путь к Xray: bin, если задан, иначе xray из PATH.
func FindBin(bin string) (string, error) {
	if bin == "" {
		bin = "xray"
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotInstalled, bin)
	}
	return p, nil
}

var versionRe = regexp.MustCompile(`^Xray (\S+)`)

// Version — версия Xray по выводу `xray version`, например "26.10.21".
func Version(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("xray version: %w: %s", err, tail(out))
	}
	m := versionRe.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("xray version: непонятный вывод %q", tail(out))
	}
	return string(m[1]), nil
}

// TestError — конфиг не прошёл `xray run -test`; Output — что сказал Xray.
type TestError struct {
	Output string
	Err    error
}

func (e *TestError) Error() string {
	return fmt.Sprintf("xray отверг конфиг: %s", e.Output)
}

func (e *TestError) Unwrap() error { return e.Err }

// Test проверяет конфиг командой `xray run -test`, не трогая рабочий
// конфиг: файл пишется во временный каталог и удаляется после проверки.
// Ассеты (geoip.dat, geosite.dat) Xray ищет как обычно: рядом с собой или
// в XRAY_LOCATION_ASSET.
func Test(ctx context.Context, bin string, config []byte) error {
	dir, err := os.MkdirTemp("", "eva-xray-test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "run", "-test", "-config", path).CombinedOutput()
	if err != nil {
		return &TestError{Output: tail(out), Err: err}
	}
	return nil
}

// tail — последние строки вывода без пустых, не больше 4 КБ.
func tail(out []byte) string {
	out = bytes.TrimSpace(out)
	const max = 4 << 10
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return strings.TrimSpace(string(out))
}
