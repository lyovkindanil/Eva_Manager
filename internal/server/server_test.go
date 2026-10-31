package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

func newServer(t *testing.T, d *db.DB, webFS fstest.MapFS) *httptest.Server {
	t.Helper()
	st := store.New(d)
	if _, err := st.EnsureLocalNode(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := &Server{DB: d, Store: st, Web: webFS, Xray: Xray{Problem: func() string { return "xray не найден" }}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return r, string(b)
}

var builtWeb = fstest.MapFS{
	"index.html":       {Data: []byte("<!doctype html><div id=root></div>")},
	"assets/app-1.js":  {Data: []byte("console.log(1)")},
	"assets/app-1.css": {Data: []byte("body{}")},
}

func TestStatus(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ts := newServer(t, d, builtWeb)
		r, body := get(t, ts.URL+"/api/status")
		if r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, body)
		}
		var st Status
		if err := json.Unmarshal([]byte(body), &st); err != nil {
			t.Fatal(err)
		}
		if st.Database.Dialect != string(d.Dialect) || st.Database.Schema < 1 {
			t.Errorf("база: %+v", st.Database)
		}
		if len(st.Nodes) != 1 || !st.Nodes[0].IsLocal {
			t.Errorf("узлы: %+v", st.Nodes)
		}
		if st.Xray.Installed || st.Xray.Running || st.Xray.Problem != "xray не найден" {
			t.Errorf("xray: %+v", st.Xray)
		}
		if r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("заголовки: %v", r.Header)
		}
	})
}

func TestHealthAndUnknownAPI(t *testing.T) {
	ts := newServer(t, dbtest.SQLite(t), builtWeb)
	if r, body := get(t, ts.URL+"/api/health"); r.StatusCode != 200 || !strings.Contains(body, `"ok"`) {
		t.Errorf("health: %d %s", r.StatusCode, body)
	}
	if r, body := get(t, ts.URL+"/api/nope"); r.StatusCode != 404 || !strings.Contains(body, "error") {
		t.Errorf("неизвестный метод: %d %s", r.StatusCode, body)
	}
	if r, _ := get(t, ts.URL+"/api/xray/logs"); r.StatusCode != 200 {
		t.Errorf("logs: %d", r.StatusCode)
	}
}

func TestStaticAndSPAFallback(t *testing.T) {
	ts := newServer(t, dbtest.SQLite(t), builtWeb)
	for _, p := range []string{"/", "/users", "/inbounds/abc"} {
		r, body := get(t, ts.URL+p)
		if r.StatusCode != 200 || !strings.Contains(body, "id=root") {
			t.Errorf("%s: %d %q", p, r.StatusCode, body)
		}
	}
	r, body := get(t, ts.URL+"/assets/app-1.js")
	if r.StatusCode != 200 || body != "console.log(1)" || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d %q %v", r.StatusCode, body, r.Header)
	}
	if r, _ := get(t, ts.URL+"/assets/missing.js"); r.StatusCode != 404 {
		t.Errorf("нет файла: %d", r.StatusCode)
	}
}

func TestPlaceholderWithoutBuild(t *testing.T) {
	ts := newServer(t, dbtest.SQLite(t), fstest.MapFS{".gitkeep": {}})
	r, body := get(t, ts.URL+"/")
	if r.StatusCode != 200 || !strings.Contains(body, "make web") {
		t.Errorf("%d %q", r.StatusCode, body)
	}
}
