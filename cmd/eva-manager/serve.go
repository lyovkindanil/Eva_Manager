package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/buildinfo"
	"github.com/lyovkindanil/Eva_Manager/internal/config"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
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
	if _, err := st.EnsureLocalNode(ctx); err != nil {
		return err
	}
	log.Info("eva-manager", "version", buildinfo.Version, "db", d.Dialect)

	core := &supervisor{log: log}
	x := server.Xray{Problem: core.problem}
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
		base, err := xray.BaseConfig(cfg.XrayAPI)
		if err != nil {
			return err
		}
		core.proc = x.Process
		core.wg.Add(1)
		go core.run(ctx, base)
		log.Info("xray", "version", v, "bin", bin)
	}

	srv := &http.Server{
		Handler:           (&server.Server{DB: d, Store: st, Xray: x, Log: log}).Handler(),
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
// с тем же конфигом, с паузой от 1 до 30 секунд.
type supervisor struct {
	log  *slog.Logger
	proc *xray.Process

	mu   sync.Mutex
	prob string
	wg   sync.WaitGroup
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

func (s *supervisor) run(ctx context.Context, config []byte) {
	defer s.wg.Done()
	backoff := time.Second
	for {
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
