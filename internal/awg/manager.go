package awg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Manager держит по процессу `eva-manager awg-run` на каждый включённый
// интерфейс: свой процесс — падение одного интерфейса не роняет панель и
// Xray. Упавший процесс перезапускается с паузой от 1 до 30 секунд.
// Настройки и пиры после каждого запуска присылает OnUp через UAPI.
type Manager struct {
	// Bin — бинарник eva-manager (обычно os.Executable()).
	Bin string
	// Args — аргументы перед флагами awg-run; по умолчанию «awg-run».
	Args      []string
	SocketDir string
	// NoNAT — не настраивать выход клиентов в интернет (тесты).
	NoNAT bool
	Log   *slog.Logger
	// OnUp — интерфейс поднят: прислать ему настройки и пиры. Ошибка —
	// процесс перезапускается.
	OnUp func(ctx context.Context, id string) error

	mu    sync.Mutex
	procs map[string]*proc
	wg    sync.WaitGroup
}

type proc struct {
	in      Interface
	cancel  context.CancelFunc
	done    chan struct{}
	gen     int
	running bool
	problem string
}

// runKey — то, что меняется только перезапуском процесса. Порт, ключи и
// маскировка применяются на лету через UAPI.
func runKey(in Interface) string {
	return fmt.Sprintf("%s|%s|%d", in.Iface, in.ServerAddr(), in.Settings.MTU)
}

// Status — состояние интерфейса для /api/status.
type Status struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Iface   string `json:"iface"`
	Running bool   `json:"running"`
	Problem string `json:"problem,omitempty"`
}

// Reconcile запускает процессы включённых интерфейсов, останавливает
// лишние и перезапускает те, у которых сменились имя, адрес или MTU.
// Новые порт, ключ или маскировка присылаются работающему процессу.
func (m *Manager) Reconcile(ctx context.Context, ifaces []Interface) {
	m.mu.Lock()
	if m.procs == nil {
		m.procs = map[string]*proc{}
	}
	want := map[string]Interface{}
	for _, in := range ifaces {
		if in.Enabled {
			want[in.ID] = in
		}
	}
	var stop []*proc
	var push []string
	for id, p := range m.procs {
		in, ok := want[id]
		if ok && runKey(in) == runKey(p.in) {
			// Сменились порт, ключ или маскировка — прислать заново (с
			// заменой пиров, сессии переподключатся). Без изменений — не
			// трогать: пиры сверяет сборщик разницей.
			if p.running && (in.Port != p.in.Port || in.Settings != p.in.Settings) {
				push = append(push, id)
			}
			p.in = in
			delete(want, id)
			continue
		}
		stop = append(stop, p)
		delete(m.procs, id)
	}
	m.mu.Unlock()
	for _, p := range stop {
		p.cancel()
		<-p.done
	}
	m.mu.Lock()
	for _, in := range want {
		pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		p := &proc{in: in, cancel: cancel, done: make(chan struct{})}
		m.procs[in.ID] = p
		m.wg.Add(1)
		go m.loop(pctx, p)
	}
	m.mu.Unlock()
	for _, id := range push {
		if err := m.OnUp(ctx, id); err != nil {
			m.log().Warn("настройки AmneziaWG", "id", id, "err", err)
		}
	}
}

// Running — сокет UAPI работающего интерфейса и номер его запуска: после
// перезапуска счётчики пиров начинаются с нуля.
func (m *Manager) Running(id string) (socket string, gen int, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.procs[id]
	if p == nil || !p.running {
		return "", 0, false
	}
	return SocketPath(m.SocketDir, p.in.Iface), p.gen, true
}

// Statuses — состояние всех процессов.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Status{}
	for _, p := range m.procs {
		out = append(out, Status{ID: p.in.ID, Name: p.in.Name, Iface: p.in.Iface, Running: p.running, Problem: p.problem})
	}
	return out
}

// Stop останавливает все процессы (интерфейсы и правила NAT убираются).
func (m *Manager) Stop() {
	m.mu.Lock()
	for id, p := range m.procs {
		p.cancel()
		delete(m.procs, id)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

func (m *Manager) set(p *proc, running bool, problem string) {
	m.mu.Lock()
	p.running = running
	p.problem = problem
	if running {
		p.gen++
	}
	m.mu.Unlock()
}

func (m *Manager) loop(ctx context.Context, p *proc) {
	defer m.wg.Done()
	defer close(p.done)
	backoff := time.Second
	for {
		started := time.Now()
		err := m.runOnce(ctx, p)
		if ctx.Err() != nil {
			m.set(p, false, "")
			return
		}
		m.set(p, false, err.Error())
		m.log().Error("AmneziaWG: процесс завершился", "iface", p.in.Iface, "err", err, "retry", backoff)
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			m.set(p, false, "")
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (m *Manager) runOnce(ctx context.Context, p *proc) error {
	m.mu.Lock()
	in := p.in
	m.mu.Unlock()
	args := append([]string(nil), m.Args...)
	if len(args) == 0 {
		args = []string{"awg-run"}
	}
	args = append(args, "-iface", in.Iface, "-addr", in.ServerAddr().String(), "-mtu", strconv.Itoa(in.Settings.MTU))
	if m.SocketDir != "" {
		args = append(args, "-socket-dir", m.SocketDir)
	}
	if m.NoNAT {
		args = append(args, "-nat=false")
	}
	cmd := exec.CommandContext(ctx, m.Bin, args...)
	cmd.SysProcAttr = sysProcAttr()
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		if errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("%w: у процесса нет права CAP_NET_ADMIN — в Docker добавьте cap_add: [NET_ADMIN] и devices: [/dev/net/tun] (см. docker-compose.yml)", err)
		}
		return err
	}
	var last lastLine
	go last.copy(out, m.log(), in.Iface)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	sock := SocketPath(m.SocketDir, in.Iface)
	if err := waitSocket(ctx, sock, exited); err != nil {
		cmd.Cancel()
		<-exited
		return fmt.Errorf("%v%s", err, last.suffix())
	}
	m.set(p, true, "")
	if err := m.OnUp(ctx, in.ID); err != nil {
		cmd.Cancel()
		<-exited
		return fmt.Errorf("настройки не применились: %w", err)
	}
	err = <-exited
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		return errors.New("процесс остановлен извне")
	}
	return fmt.Errorf("%v%s", err, last.suffix())
}

// waitSocket ждёт, пока процесс откроет UAPI-сокет.
func waitSocket(ctx context.Context, sock string, exited chan error) error {
	deadline := time.After(15 * time.Second)
	for {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			return nil
		}
		select {
		case err := <-exited:
			exited <- err
			if err == nil {
				err = fmt.Errorf("процесс вышел")
			}
			return err
		case <-deadline:
			return fmt.Errorf("сокет %s не появился за 15 с", sock)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// lastLine пересылает вывод процесса в журнал и помнит последнюю строку:
// она объясняет падение.
type lastLine struct {
	mu   sync.Mutex
	line string
}

func (l *lastLine) copy(r io.Reader, log *slog.Logger, iface string) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Текстовый slog процесса: msg="…" → …
		if rest, ok := strings.CutPrefix(line, "msg="); ok {
			if q, err := strconv.QuotedPrefix(rest); err == nil {
				if uq, err := strconv.Unquote(q); err == nil {
					rest = uq + rest[len(q):]
				}
			}
			line = rest
		}
		l.mu.Lock()
		l.line = line
		l.mu.Unlock()
		log.Info("awg-run: " + line)
	}
}

func (l *lastLine) suffix() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.line == "" {
		return ""
	}
	return ": " + l.line
}

// ParseAddr — адрес сервера из флага -addr.
func ParseAddr(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("адрес %q: ожидался IPv4 с маской, например 10.8.0.1/24", s)
	}
	return p, nil
}
