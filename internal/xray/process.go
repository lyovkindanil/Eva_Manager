package xray

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Process — Xray, запущенный панелью. Журнал процесса (stdout и stderr)
// хранится в памяти, последние LogLines строк.
type Process struct {
	Bin string
	// ConfigPath — рабочий конфиг; Start пишет его перед запуском.
	ConfigPath string

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	exitErr error
	// stopping — Stop остановил процесс сам: это не ошибка.
	stopping bool
	started  time.Time
	logs     *ring
}

// LogLines — сколько строк журнала Xray держать в памяти.
const LogLines = 500

// NewProcess готовит процесс; запускает его Start.
func NewProcess(bin, configPath string) *Process {
	return &Process{Bin: bin, ConfigPath: configPath, logs: newRing(LogLines)}
}

// State — состояние процесса для API панели.
type State struct {
	Running bool      `json:"running"`
	PID     int       `json:"pid,omitempty"`
	Since   time.Time `json:"since,omitempty"`
	// LastError — почему процесс завершился в прошлый раз.
	LastError string `json:"last_error,omitempty"`
}

// ErrRunning — процесс уже запущен.
var ErrRunning = errors.New("xray уже запущен")

// Start проверяет конфиг (`xray run -test`), записывает его в ConfigPath и
// запускает Xray. Если за startGrace процесс завершился, возвращает ошибку с
// его выводом: так видно, например, занятый порт.
func (p *Process) Start(ctx context.Context, config []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running() {
		return ErrRunning
	}
	if err := Test(ctx, p.Bin, config); err != nil {
		return err
	}
	if err := writeFileAtomic(p.ConfigPath, config); err != nil {
		return fmt.Errorf("конфиг xray: %w", err)
	}
	cmd := exec.Command(p.Bin, "run", "-config", p.ConfigPath)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("запуск xray: %w", err)
	}
	done := make(chan struct{})
	p.cmd, p.done, p.exitErr, p.stopping, p.started = cmd, done, nil, false, time.Now()
	go p.collect(pr)
	go func() {
		err := cmd.Wait()
		pw.Close()
		p.mu.Lock()
		switch {
		case p.stopping:
			err = nil
		case err == nil:
			err = errors.New("xray завершился")
		}
		p.exitErr = err
		p.mu.Unlock()
		close(done)
	}()

	p.mu.Unlock()
	select {
	case <-done:
	case <-time.After(startGrace):
	case <-ctx.Done():
	}
	p.mu.Lock()
	if !p.running() {
		return fmt.Errorf("xray завершился сразу после запуска: %v: %s", p.exitErr, p.logs.tail(10))
	}
	return nil
}

// startGrace — сколько ждать, не упадёт ли Xray сразу после запуска.
var startGrace = 1500 * time.Millisecond

// Stop останавливает Xray: SIGTERM, через 5 секунд — SIGKILL.
func (p *Process) Stop() error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	running := p.running()
	if running {
		p.stopping = true
	}
	p.mu.Unlock()
	if !running {
		return nil
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	return nil
}

// Restart — Stop, затем Start с новым конфигом.
func (p *Process) Restart(ctx context.Context, config []byte) error {
	if err := Test(ctx, p.Bin, config); err != nil {
		return err // рабочий Xray не трогаем
	}
	if err := p.Stop(); err != nil {
		return err
	}
	return p.Start(ctx, config)
}

// State — запущен ли Xray и почему остановился.
func (p *Process) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := State{Running: p.running()}
	if s.Running {
		s.PID, s.Since = p.cmd.Process.Pid, p.started
	} else if p.exitErr != nil {
		s.LastError = p.exitErr.Error()
	}
	return s
}

// Logs — последние строки вывода Xray, старые первыми.
func (p *Process) Logs() []string { return p.logs.lines() }

// Done закрывается, когда текущий процесс завершился; nil — не запускался.
func (p *Process) Done() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

func (p *Process) running() bool {
	if p.done == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *Process) collect(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		p.logs.add(sc.Text())
	}
	_, _ = io.Copy(io.Discard, r)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ring — кольцевой буфер строк журнала.
type ring struct {
	mu   sync.Mutex
	buf  []string
	next int
	full bool
}

func newRing(n int) *ring { return &ring{buf: make([]string, n)} }

func (r *ring) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = s
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

func (r *ring) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]string(nil), r.buf[:r.next]...)
	}
	return append(append([]string(nil), r.buf[r.next:]...), r.buf[:r.next]...)
}

func (r *ring) tail(n int) []string {
	l := r.lines()
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return l
}
