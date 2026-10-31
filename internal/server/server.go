// Package server — HTTP API панели и раздача веб-интерфейса.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/buildinfo"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	"github.com/lyovkindanil/Eva_Manager/internal/web"
	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// Xray — что серверу нужно знать о ядре; nil-поля — Xray не установлен.
type Xray struct {
	// Version — версия из `xray version`; пусто — Xray не найден.
	Version string
	// Problem — почему Xray не работает (не найден, конфиг отвергнут…).
	Problem func() string
	Process *xray.Process
	API     *xray.API
}

// Server — обработчики HTTP.
type Server struct {
	DB    *db.DB
	Store *store.Store
	Xray  Xray
	// Web — файлы интерфейса; по умолчанию встроенные.
	Web fs.FS
	Log *slog.Logger
}

// Handler — все маршруты панели.
func (s *Server) Handler() http.Handler {
	if s.Web == nil {
		s.Web = web.FS()
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/xray/logs", s.xrayLogs)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "нет такого метода API")
	})
	mux.Handle("/", s.static())
	return securityHeaders(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.DB.PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "база недоступна")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Status — ответ /api/status.
type Status struct {
	Version  string       `json:"version"`
	Database DBStatus     `json:"database"`
	Xray     XrayStatus   `json:"xray"`
	Nodes    []store.Node `json:"nodes"`
}

type DBStatus struct {
	Dialect string `json:"dialect"`
	Schema  int    `json:"schema"`
}

type XrayStatus struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	xray.State
	// Problem — что мешает работе Xray, по-русски для интерфейса.
	Problem string `json:"problem,omitempty"`
	// API — отвечает ли gRPC API.
	API    bool  `json:"api"`
	Uptime int64 `json:"uptime_s,omitempty"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	schema, err := s.DB.SchemaVersion(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	nodes, err := s.Store.Nodes(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	st := Status{
		Version:  buildinfo.Version,
		Database: DBStatus{Dialect: string(s.DB.Dialect), Schema: schema},
		Nodes:    nodes,
	}
	x := &st.Xray
	x.Installed = s.Xray.Version != ""
	x.Version = s.Xray.Version
	if s.Xray.Process != nil {
		x.State = s.Xray.Process.State()
	}
	if s.Xray.Problem != nil {
		x.Problem = s.Xray.Problem()
	}
	if s.Xray.API != nil && x.Running {
		actx, cancel := context.WithTimeout(ctx, 2*time.Second)
		sys, err := s.Xray.API.SysStats(actx)
		cancel()
		if err == nil {
			x.API, x.Uptime = true, int64(sys.Uptime/time.Second)
		} else if x.Problem == "" {
			x.Problem = "Xray запущен, но gRPC API не отвечает"
		}
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) xrayLogs(w http.ResponseWriter, r *http.Request) {
	lines := []string{}
	if s.Xray.Process != nil {
		lines = s.Xray.Process.Logs()
	}
	writeJSON(w, http.StatusOK, map[string][]string{"lines": lines})
}

// static отдаёт интерфейс. Неизвестный путь без расширения — это маршрут
// приложения (/users, /inbounds/…), на него отдаётся index.html.
func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "метод не поддерживается")
			return
		}
		if !web.Built(s.Web) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(placeholder))
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "index.html" {
			s.serveIndex(w, r)
			return
		}
		if _, err := fs.Stat(s.Web, p); err != nil {
			if errors.Is(err, fs.ErrNotExist) && path.Ext(p) == "" {
				s.serveIndex(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			// У Vite в именах файлов хеш содержимого.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.Web, "index.html")
}

const placeholder = `<!doctype html><html lang="ru"><meta charset="utf-8"><title>Eva Manager</title>
<body style="font:15px system-ui;margin:3em;background:#16181d;color:#e8eaed">
<h1>Eva Manager</h1><p>Интерфейс не собран: выполните <code>make web</code> и пересоберите панель.
API работает: <a style="color:#2bd49a" href="/api/status">/api/status</a>.</p></body></html>`

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.Log.Error("ошибка API", "err", err)
	writeError(w, http.StatusInternalServerError, "внутренняя ошибка панели")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
