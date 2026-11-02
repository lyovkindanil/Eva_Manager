package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

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
	var serverPanel server.Panel // nil — Xray не найден, применение недоступно
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
		core.wg.Add(1)
		go core.run(ctx)
		log.Info("xray", "version", v, "bin", bin)

		// Сборщик: сверяет пользователей с ядром, забирает трафик,
		// применяет лимиты и сроки. Supervisor как Core даёт горячее
		// применение конфига без перезапуска панели.
		pn := &panel.Panel{Store: st, API: x.API, Core: core, APIAddr: cfg.XrayAPI, NodeID: node.ID, Log: log}
		core.wg.Add(1)
		go func() {
			defer core.wg.Done()
			pn.Run(ctx)
		}()
		serverPanel = pn
	}

	srv := &http.Server{
		Handler:           (&server.Server{DB: d, Store: st, Xray: x, Panel: serverPanel, Log: log}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	log.Info("панель слушает", "addr", "http://"+ln.Addr().String())
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("остановка")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	err = srv.Shutdown(sctx)
	core.stop()
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
}

// applyWatch — сколько ждать после перезапуска, не упадёт ли ядро с
// новым конфигом; упало — откат на прежний. Переменная — в тестах
// интервал сокращается.
var applyWatch = 10 * time.Second

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
		config := s.cfg
		s.mu.Unlock()
		if err := s.proc.Start(ctx, config); err != nil && !errors.Is(err, xray.ErrRunning) {
			s.setProblem(err.Error())
			s.log.Error("xray не запустился", "err", err, "retry", backoff)
		} else {
			s.setProblem("")
			started := time.Now()
			select {
			case <-ctx.Done():
				return
			case <-s.proc.Done():
			}
			if ctx.Err() != nil {
				return
			}
			st := s.proc.State()
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
		_ = s.proc.Stop()
	}
	s.wg.Wait()
}
