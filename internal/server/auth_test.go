package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
)

func TestAuth(t *testing.T) {
	ts := newServer(t, dbtest.SQLite(t), builtWeb)
	for _, path := range []string{"/api/status", "/api/xray/logs", "/api/нет"} {
		for _, h := range []string{"", "Bearer", "Bearer wrong", "Basic " + testToken} {
			req, _ := http.NewRequest("GET", ts.URL+path, nil)
			if h != "" {
				req.Header.Set("Authorization", h)
			}
			r, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			r.Body.Close()
			if r.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %q: %d", path, h, r.StatusCode)
			}
		}
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/xray/apply", nil)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusUnauthorized {
		t.Errorf("apply без токена: %v %v", r, err)
	}
	// health, интерфейс и подписки — без токена.
	for path, code := range map[string]int{"/api/health": 200, "/": 200, "/sub/нет": 404} {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != code {
			t.Errorf("%s: %d", path, r.StatusCode)
		}
	}
}

func TestSubHandlerOnlySubscriptions(t *testing.T) {
	d := dbtest.SQLite(t)
	s := &Server{DB: d, Token: testToken}
	ts := httptest.NewServer(s.SubHandler())
	defer ts.Close()
	for _, path := range []string{"/api/status", "/api/health", "/"} {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s на адресе подписок: %d", path, r.StatusCode)
		}
	}
}

func TestLoadToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "admin.token")
	a, err := LoadToken(p)
	if err != nil || len(a) < 20 {
		t.Fatal(a, err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("права %v", st.Mode())
	}
	if b, _ := LoadToken(p); b != a {
		t.Error("токен поменялся")
	}
}
