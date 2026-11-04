package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/buildinfo"
	"github.com/lyovkindanil/Eva_Manager/internal/config"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/panel"
	"github.com/lyovkindanil/Eva_Manager/internal/server"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

func serve(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	token, err := server.LoadToken(cfg.TokenFile())
	if err != nil {
		return fmt.Errorf("токен администратора: %w", err)
	}
	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer d.Close()
	st := store.New(d)
	node, err := st.EnsureLocalNode(ctx)
	if err != nil {
		return err
	}
	log.Info("eva-manager", "version", buildinfo.Version, "db", d.Dialect)

	core := &supervisor{log: log}
	x := server.Xray{Problem: core.problem}
	pn := &panel.Panel{Store: st, NodeID: node.ID, Log: log}
	if bin, err := xray.FindBin(cfg.XrayBin); err != nil {
		core.setProblem("Xray не найден: укажите путь в EVA_XRAY_BIN или установите xray в PATH")
		log.Warn("xray не найден, панель работает без ядра", "err", err)
	} else if v, err := xray.Version(ctx, bin); err != nil {
		core.setProblem("Xray не запускается: " + err.Error())
		log.Error("xray", "err", err)
	} else {
		x.Version = v
		x.Process = xray.NewProcess(bin, filepath.Join(cfg.DataDir, "xray", "config.json"))
		if x.API, err = xray.Dial(cfg.XrayAPI); err != nil {
			return err
		}
		defer x.API.Close()
		inbs, err := st.Inbounds(ctx, node.ID)
		if err != nil {
			return err
		}
		base, err := inbound.BuildConfig(cfg.XrayAPI, inbs)
		if err != nil {
			return err
		}
		core.proc = x.Process
		core.cfg = base
		log.Info("xray", "version", v, "bin", bin)

		// Supervisor как Core даёт горячее применение конфига без
		// перезапуска панели.
		pn.API, pn.Core, pn.APIAddr = x.API, core, cfg.XrayAPI
		core.onStart = func(ctx context.Context) {
			if err := pn.SyncUsers(ctx); err != nil {
				log.Warn("пользователи после запуска ядра", "err", err)
			}
		}
		core.beforeStop = func(ctx context.Context) {
			if err := pn.CollectOnce(ctx); err != nil {
				log.Warn("трафик перед остановкой ядра", "err", err)
			}
		}
		core.wg.Add(1)
		go core.run(ctx)
	}

	// AmneziaWG: по процессу `eva-manager awg-run` на интерфейс.
	var mgr *awg.Manager
	if runtime.GOOS == "linux" {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		// eva-awg рядом — копия бинарника с CAP_NET_ADMIN (так в
		// Docker-образе): права на сеть получает только процесс
		// интерфейса, сама панель работает без них.
		if p := filepath.Join(filepath.Dir(self), "eva-awg"); fileExists(p) {
			self = p
		}
		mgr = &awg.Manager{Bin: self, SocketDir: cfg.AWGSocketDir, Log: log, OnUp: pn.PushAWG}
		pn.AWG = mgr
		defer mgr.Stop()
	}

	// Сборщик: сверяет пользователей с ядрами, забирает трафик,
	// применяет лимиты и сроки.
	core.wg.Add(1)
	go func() {
		defer core.wg.Done()
		pn.Run(ctx)
	}()
	var serverPanel server.Panel = pn

	srv := &server.Server{DB: d, Store: st, Xray: x, Panel: serverPanel, Token: token, Log: log,
		SubURL: cfg.SubURL, SubListen: cfg.SubListen}
	if mgr != nil {
		srv.AWG = mgr.Statuses
	}
	servers := []*http.Server{newHTTP(srv.Handler())}
	addrs := []string{cfg.Listen}
	if cfg.SubListen != "" {
		servers = append(servers, newHTTP(srv.SubHandler()))
		addrs = append(addrs, cfg.SubListen)
	}
	errc := make(chan error, len(servers))
	for i, hs := range servers {
		ln, err := net.Listen("tcp", addrs[i])
		if err != nil {
			return err
		}
		what := "панель слушает"
		if i > 0 {
			what = "подписки слушают"
		}
		log.Info(what, "addr", "http://"+ln.Addr().String())
		go func() { errc <- hs.Serve(ln) }()
	}

	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	log.Info("остановка")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	for _, hs := range servers {
		if e := hs.Shutdown(sctx); e != nil && (err == nil || errors.Is(err, http.ErrServerClosed)) {
			err = e
		}
	}
	core.stop()
	if mgr != nil {
		// Без Xray трафик AmneziaWG перед остановкой забрать некому:
		// supervisor делает это только вместе с ядром.
		if core.proc == nil {
			cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
			if e := pn.CollectOnce(cctx); e != nil {
				log.Warn("трафик AmneziaWG перед остановкой", "err", e)
			}
			ccancel()
		}
		mgr.Stop()
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// supervisor держит Xray запущенным: если ядро упало, перезапускает его
// с тем же конфигом, с паузой от 1 до 30 секунд. Он же владеет текущим
// конфигом и применяет новый (panel.Core): с проверкой `xray run -test`
// и откатом, если ядро с новым конфигом падает.
type supervisor struct {
	log  *slog.Logger
	proc *xray.Process

	mu  sync.Mutex // cfg и prob
	cfg []byte     // текущий конфиг ядра

	prob string
	wg   sync.WaitGroup

	// onStart вызывается после каждого запуска ядра: при старте, после
	// падения, после применения и отката. В конфиге пользователей нет,
	// поэтому без этого запущенное заново ядро до следующего тика
	// сборщика (до 30 секунд) не пускало бы никого.
	onStart func(context.Context)
	// beforeStop вызывается перед каждой остановкой ядра, пока оно живо:
	// счётчики трафика в ядре с прошлого тика иначе пропали бы.
	beforeStop func(context.Context)
}

func (s *supervisor) started(ctx context.Context) {
	if s.onStart != nil && s.proc.State().Running {
		s.onStart(ctx)
	}
}

func (s *supervisor) stopping(ctx context.Context) {
	if s.beforeStop != nil && s.proc.State().Running {
		s.beforeStop(ctx)
	}
}

// applyWatch — сколько ждать после перезапуска, не упадёт ли ядро с
// новым конфигом; упало — откат на прежний. Переменная — в тестах
// интервал сокращается.
var applyWatch = 10 * time.Second

func newHTTP(h http.Handler) *http.Server {
	return &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
}

func (s *supervisor) setProblem(p string) {
	s.mu.Lock()
	s.prob = p
	s.mu.Unlock()
}

func (s *supervisor) problem() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prob
}

// Apply — panel.Core: проверить конфиг и перезапустить с ним ядро.
// Пока идёт применение, run-цикл ждёт на мьютексе — гонки перезапусков
// нет. Конфиг, не прошедший `xray run -test`, рабочее ядро не трогает;
// ядро, упавшее сразу после старта или в первые applyWatch секунд,
// откатывается на прежний конфиг.
func (s *supervisor) Apply(ctx context.Context, config []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.cfg
	// Restart сначала проверяет конфиг (`xray run -test`); трафик
	// забираем только если конфиг годен и ядро правда будет остановлено.
	if err := xray.Test(ctx, s.proc.Bin, config); err != nil {
		return err
	}
	s.stopping(ctx)
	defer s.started(ctx)
	if err := s.proc.Restart(ctx, config); err != nil {
		// Restart не прошёл проверку — ядро ещё живо; прошёл, но не
		// запустился (например, занят порт) — возвращаем прежний конфиг.
		if !s.proc.State().Running && old != nil {
			if rerr := s.proc.Start(ctx, old); rerr != nil {
				s.prob = "откат конфига не запустился: " + rerr.Error()
				s.log.Error("откат конфига", "err", rerr)
			}
		}
		return err
	}
	s.cfg = config
	select {
	case <-s.proc.Done():
		st := s.proc.State()
		s.cfg = old
		s.prob = "конфиг откачен: " + st.LastError
		if err := s.proc.Start(ctx, old); err != nil {
			s.prob += "; откат не запустился: " + err.Error()
			s.log.Error("откат конфига", "err", err)
		}
		return fmt.Errorf("xray упал с новым конфигом: %s", st.LastError)
	case <-time.After(applyWatch):
	case <-ctx.Done():
		return ctx.Err()
	}
	s.prob = ""
	return nil
}

func (s *supervisor) run(ctx context.Context) {
	defer s.wg.Done()
	backoff := time.Second
	for {
		s.mu.Lock()
		err := s.proc.Start(ctx, s.cfg)
		s.mu.Unlock()
		if err != nil && !errors.Is(err, xray.ErrRunning) {
			s.setProblem(err.Error())
			s.log.Error("xray не запустился", "err", err, "retry", backoff)
		} else {
			if err == nil {
				s.setProblem("")
				s.started(ctx)
			}
			started := time.Now()
			select {
			case <-ctx.Done():
				return
			case <-s.proc.Done():
			}
			if ctx.Err() != nil {
				return
			}
			// Ядро могло остановить Apply: тогда, дождавшись мьютекса,
			// видим уже новый процесс — это не падение.
			s.mu.Lock()
			st := s.proc.State()
			s.mu.Unlock()
			if st.Running {
				backoff = time.Second
				continue
			}
			s.setProblem("Xray упал, перезапуск: " + st.LastError)
			s.log.Error("xray завершился", "err", st.LastError)
			if time.Since(started) > time.Minute {
				backoff = time.Second // долго работал — не наказываем
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (s *supervisor) stop() {
	if s.proc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.mu.Lock()
		s.stopping(ctx)
		_ = s.proc.Stop()
		s.mu.Unlock()
		cancel()
	}
	s.wg.Wait()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
