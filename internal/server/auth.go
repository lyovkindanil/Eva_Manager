package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

// LoadToken читает токен администратора из файла, а если файла нет —
// создаёт новый (права 0600). Токен — доступ к API для CLI и скриптов и
// подтверждение хозяина в мастере первого запуска.
func LoadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		tok := strings.TrimSpace(string(b))
		if len(tok) < 20 {
			return "", fmt.Errorf("токен в %s короче 20 символов", path)
		}
		return tok, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		// Файл только что создал другой процесс (панель или
		// eva-manager token): берём его токен.
		time.Sleep(50 * time.Millisecond)
		return LoadToken(path)
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(tok + "\n"); err != nil {
		return "", err
	}
	return tok, f.Close()
}

// sessionCookie — cookie входа в веб-интерфейс.
const sessionCookie = "eva_session"

// csrfHeader — его обязан слать интерфейс в изменяющих запросах с cookie:
// чужая страница не может добавить свой заголовок без CORS, которого у
// панели нет, а SameSite=Strict закрывает остальное.
const csrfHeader = "X-Eva-Request"

type ctxKey struct{}

// adminFrom — вошедший администратор; nil — запрос с токеном CLI.
func adminFrom(ctx context.Context) *store.Admin {
	a, _ := ctx.Value(ctxKey{}).(*store.Admin)
	return a
}

// requireToken пропускает запрос с заголовком Authorization: Bearer
// <токен> (CLI, скрипты) или с cookie живой сессии (веб-интерфейс).
func (s *Server) requireToken(next http.Handler) http.Handler {
	want := []byte(s.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			if len(want) > 0 && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), want) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		} else if c, err := r.Cookie(sessionCookie); err == nil {
			a, err := s.Store.SessionAdmin(r.Context(), c.Value)
			if err == nil {
				if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) == "" {
					writeError(w, http.StatusForbidden, "запрос без заголовка "+csrfHeader)
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
				return
			}
			if !errors.Is(err, store.ErrNotFound) {
				s.fail(w, err)
				return
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="eva-manager"`)
		writeError(w, http.StatusUnauthorized, "нужен вход")
	})
}

// loginLimiter — не больше loginBurst неудачных входов с адреса за
// loginWindow: перебор пароля упирается в 429.
type loginLimiter struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

const (
	loginBurst  = 5
	loginWindow = 5 * time.Minute
)

func (l *loginLimiter) recent(ip string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fail[ip] {
		if now.Sub(t) < loginWindow {
			keep = append(keep, t)
		}
	}
	return keep
}

func (l *loginLimiter) allowed(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail == nil {
		l.fail = map[string][]time.Time{}
	}
	keep := l.recent(ip, now)
	if len(keep) == 0 {
		delete(l.fail, ip)
	} else {
		l.fail[ip] = keep
	}
	return len(keep) < loginBurst
}

func (l *loginLimiter) failed(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail == nil {
		l.fail = map[string][]time.Time{}
	}
	// Не копим память на тысячах адресов: старые записи вычищаются.
	if len(l.fail) > 10000 {
		for k := range l.fail {
			if len(l.recent(k, now)) == 0 {
				delete(l.fail, k)
			}
		}
	}
	l.fail[ip] = append(l.recent(ip, now), now)
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.fail, ip)
	l.mu.Unlock()
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Token — токен администратора: им мастер первого запуска
	// подтверждает, что настраивает хозяин сервера.
	Token string `json:"token,omitempty"`
}

// setupState — GET /api/setup: нужен ли мастер первого запуска.
func (s *Server) setupState(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.AdminCount(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needed": n == 0})
}

// setup — POST /api/setup: первый администратор. Требует токен из
// eva-manager token: открытую наружу панель до настройки не захватить.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	ip := clientIP(r)
	now := time.Now()
	if !s.limiter.allowed(ip, now) {
		writeError(w, http.StatusTooManyRequests, "слишком много попыток, подождите 5 минут")
		return
	}
	if len(s.Token) == 0 || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(c.Token)), []byte(s.Token)) != 1 {
		s.limiter.failed(ip, now)
		writeError(w, http.StatusForbidden, "неверный токен: на сервере выполните eva-manager token")
		return
	}
	a, err := s.Store.CreateFirstAdmin(r.Context(), c.Username, c.Password)
	if errors.Is(err, store.ErrAdminExists) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Log.Info("создан администратор", "username", a.Username, "ip", ip)
	s.startSession(w, r, a)
}

// login — POST /api/login.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	ip := clientIP(r)
	now := time.Now()
	if !s.limiter.allowed(ip, now) {
		writeError(w, http.StatusTooManyRequests, "слишком много попыток, подождите 5 минут")
		return
	}
	a, err := s.Store.Login(r.Context(), c.Username, c.Password)
	if errors.Is(err, store.ErrBadLogin) {
		s.limiter.failed(ip, now)
		s.Log.Warn("неудачный вход", "username", c.Username, "ip", ip)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.limiter.reset(ip)
	s.startSession(w, r, a)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, a *store.Admin) {
	tok, exp, err := s.Store.CreateSession(r.Context(), a.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", Expires: exp,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r),
	})
	writeJSON(w, http.StatusOK, a)
}

// isHTTPS — запрос пришёл по HTTPS (сам или через обратный прокси).
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// logout — POST /api/logout.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Store.DeleteSession(r.Context(), c.Value); err != nil {
			s.fail(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// me — GET /api/me: кто вошёл.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r.Context())
	if a == nil {
		writeJSON(w, http.StatusOK, map[string]string{"username": "", "via": "token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": a.Username, "via": "session"})
}

// readJSON разбирает тело запроса (до 1 МиБ); ошибка — 400.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "неверный JSON: "+err.Error())
		return false
	}
	return true
}
