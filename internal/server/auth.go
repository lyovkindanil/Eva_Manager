package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadToken читает токен администратора из файла, а если файла нет —
// создаёт новый (права 0600). До входа по паролю (этап 3) API защищено
// только этим токеном.
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

// requireToken пропускает запрос с заголовком Authorization: Bearer <токен>.
func (s *Server) requireToken(next http.Handler) http.Handler {
	want := []byte(s.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(want) == 0 || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="eva-manager"`)
			writeError(w, http.StatusUnauthorized, "нужен токен администратора: eva-manager token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
