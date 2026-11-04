package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	vpnparser "github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/profile"
)

// fakePanel считает вызовы: API сразу сверяет пользователей и применяет
// конфиг, не дожидаясь тика сборщика.
type fakePanel struct{ applies, syncs int }

func (f *fakePanel) ApplyConfig(context.Context) error { f.applies++; return nil }
func (f *fakePanel) SyncUsers(context.Context) error   { f.syncs++; return nil }

// browser — клиент с cookie, как веб-интерфейс.
type browser struct {
	t    *testing.T
	base string
	c    *http.Client
}

func newBrowser(t *testing.T, base string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: base, c: &http.Client{Jar: jar}}
}

func (b *browser) do(method, path string, body any, csrf bool) (int, string) {
	b.t.Helper()
	var r io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, b.base+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf {
		req.Header.Set(csrfHeader, "1")
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func (b *browser) json(method, path string, body, into any) int {
	b.t.Helper()
	code, out := b.do(method, path, body, true)
	if into != nil && code < 300 {
		if err := json.Unmarshal([]byte(out), into); err != nil {
			b.t.Fatalf("%s %s: %v\n%s", method, path, err, out)
		}
	}
	if code >= 300 && into != nil {
		b.t.Logf("%s %s: %d %s", method, path, code, out)
	}
	return code
}

func newAPIServer(t *testing.T, d *db.DB) (*httptest.Server, *fakePanel) {
	t.Helper()
	st := store.New(d)
	if _, err := st.EnsureLocalNode(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &fakePanel{}
	s := &Server{DB: d, Store: st, Web: builtWeb, Token: testToken, Panel: p, Xray: Xray{Problem: func() string { return "" }}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, p
}

// Первый запуск: мастер требует токен с сервера, создаёт администратора
// один раз; дальше вход по паролю, cookie, CSRF-заголовок и выход.
func TestSetupAndLogin(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ts, _ := newAPIServer(t, d)
		b := newBrowser(t, ts.URL)
		var st struct{ Needed bool }
		if b.json("GET", "/api/setup", nil, &st); !st.Needed {
			t.Fatal("мастер не нужен на пустой базе")
		}
		if code, _ := b.do("GET", "/api/users", nil, false); code != 401 {
			t.Fatalf("без входа: %d", code)
		}
		if code, out := b.do("POST", "/api/setup", map[string]string{"username": "admin", "password": "очень-длинный-пароль", "token": "чужой"}, true); code != 403 {
			t.Fatalf("мастер с чужим токеном: %d %s", code, out)
		}
		if code, out := b.do("POST", "/api/setup", map[string]string{"username": "admin", "password": "short", "token": testToken}, true); code != 400 {
			t.Fatalf("короткий пароль: %d %s", code, out)
		}
		if code, out := b.do("POST", "/api/setup", map[string]string{"username": "admin", "password": "очень-длинный-пароль", "token": testToken}, true); code != 200 {
			t.Fatalf("мастер: %d %s", code, out)
		}
		// Сессия уже открыта.
		var me struct{ Username, Via string }
		if b.json("GET", "/api/me", nil, &me); me.Username != "admin" || me.Via != "session" {
			t.Fatalf("%+v", me)
		}
		// Второй раз мастер не срабатывает, даже с токеном.
		if code, _ := newBrowser(t, ts.URL).do("POST", "/api/setup", map[string]string{"username": "evil", "password": "очень-длинный-пароль", "token": testToken}, true); code != 409 {
			t.Fatalf("повторный мастер: %d", code)
		}
		// Изменение без CSRF-заголовка — 403.
		if code, _ := b.do("POST", "/api/users", map[string]string{"name": "x"}, false); code != 403 {
			t.Fatalf("без CSRF: %d", code)
		}
		if code, _ := b.do("POST", "/api/logout", nil, true); code != 200 {
			t.Fatal("выход")
		}
		if code, _ := b.do("GET", "/api/users", nil, false); code != 401 {
			t.Fatalf("после выхода: %d", code)
		}

		// Вход: неверный пароль, затем верный.
		b2 := newBrowser(t, ts.URL)
		if code, _ := b2.do("POST", "/api/login", map[string]string{"username": "admin", "password": "не тот пароль"}, true); code != 401 {
			t.Fatal("неверный пароль принят")
		}
		if code, _ := b2.do("POST", "/api/login", map[string]string{"username": "admin", "password": "очень-длинный-пароль"}, true); code != 200 {
			t.Fatal("верный пароль не принят")
		}
		if code, _ := b2.do("GET", "/api/users", nil, false); code != 200 {
			t.Fatal("после входа")
		}
		// Перебор: после 5 неудач — 429 даже с верным паролем.
		b3 := newBrowser(t, ts.URL)
		for range loginBurst {
			b3.do("POST", "/api/login", map[string]string{"username": "admin", "password": "перебор-перебор"}, true)
		}
		if code, _ := b3.do("POST", "/api/login", map[string]string{"username": "admin", "password": "очень-длинный-пароль"}, true); code != 429 {
			t.Fatalf("перебор не ограничен: %d", code)
		}
	})
}

func loggedIn(t *testing.T, ts *httptest.Server) *browser {
	b := newBrowser(t, ts.URL)
	if code, out := b.do("POST", "/api/setup", map[string]string{"username": "admin", "password": "очень-длинный-пароль", "token": testToken}, true); code != 200 {
		t.Fatalf("мастер: %d %s", code, out)
	}
	return b
}

// Пользователи, инбаунды и AmneziaWG из интерфейса: ссылки сразу
// разбирает парсер VPN4Eva, изменения сразу уходят в ядра.
func TestResourcesAPI(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ts, p := newAPIServer(t, d)
		b := loggedIn(t, ts)

		var ib applyResult
		if code := b.json("POST", "/api/inbounds", map[string]any{"name": "NL-1", "host": "vpn.example.com", "encryption": true}, &ib); code != 201 || !ib.Applied {
			t.Fatalf("инбаунд: %d %+v", code, ib)
		}
		if code := b.json("POST", "/api/inbounds", map[string]any{"name": "NL-2", "host": "vpn.example.com", "port": 443}, nil); code != 409 {
			t.Fatalf("занятый порт: %d", code)
		}
		var awgv AWGView
		if code := b.json("POST", "/api/awg", map[string]any{"host": "vpn.example.com"}, &awgv); code != 201 || awgv.Iface != "awg0" || awgv.Port != 51820 {
			t.Fatalf("awg: %d %+v", code, awgv)
		}

		expires := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
		var u UserView
		if code := b.json("POST", "/api/users", map[string]any{"name": "Даша", "traffic_limit": 10 << 30, "expires_at": expires}, &u); code != 201 {
			t.Fatal(code)
		}
		if len(u.Access) != 2 || u.Access[0].Kind != "vless" || u.Access[1].Kind != "awg" || !strings.HasPrefix(u.SubURL, ts.URL+"/sub/") {
			t.Fatalf("%+v", u)
		}
		for _, a := range u.Access {
			if _, err := vpnparser.ParseServer(a.Link); err != nil {
				t.Fatalf("%s: %v", a.Kind, err)
			}
		}
		if code := b.json("POST", "/api/users", map[string]any{"name": "Даша"}, nil); code != 409 {
			t.Fatalf("дубль имени: %d", code)
		}
		code, conf := b.do("GET", "/api/users/"+u.ID+"/awg/"+u.Access[1].AWG, nil, false)
		if code != 200 || !strings.Contains(conf, "[Interface]") {
			t.Fatalf("conf: %d %s", code, conf)
		}

		// Выключение, продление, сброс трафика, новая подписка.
		var u2 UserView
		f := false
		b.json("PATCH", "/api/users/"+u.ID, map[string]any{"enabled": f, "note": "заметка"}, &u2)
		if u2.Status != store.UserDisabled || u2.Note != "заметка" {
			t.Fatalf("%+v", u2.User)
		}
		b.json("PATCH", "/api/users/"+u.ID, map[string]any{"enabled": true, "expires_at": ""}, &u2)
		if u2.Status != store.UserActive || !u2.ExpiresAt.IsZero() {
			t.Fatalf("%+v", u2.User)
		}
		old := u2.SubURL
		b.json("POST", "/api/users/"+u.ID+"/rotate-sub", nil, &u2)
		if u2.SubURL == old {
			t.Fatal("подписка не перевыпущена")
		}
		if code, _ := http.Get(old); code.StatusCode != 404 {
			t.Fatalf("старая подписка: %d", code.StatusCode)
		}

		var list []store.User
		if b.json("GET", "/api/users", nil, &list); len(list) != 1 {
			t.Fatalf("%+v", list)
		}
		syncsBefore := p.syncs
		if code := b.json("DELETE", "/api/users/"+u.ID, nil, nil); code != 200 {
			t.Fatal(code)
		}
		if p.syncs == syncsBefore {
			t.Error("удаление не сверено с ядрами")
		}
		if code := b.json("DELETE", "/api/awg/"+awgv.ID, nil, nil); code != 200 {
			t.Fatal(code)
		}

		// Адрес подписок из настроек.
		var set Settings
		if code := b.json("PUT", "/api/settings", map[string]any{"sub_url": "ftp://x"}, nil); code != 400 {
			t.Fatalf("неверный адрес: %d", code)
		}
		b.json("PUT", "/api/settings", map[string]any{"sub_url": "https://vpn.example.com:2096/"}, &set)
		if set.SubURL != "https://vpn.example.com:2096" {
			t.Fatalf("%+v", set)
		}
		b.json("POST", "/api/users", map[string]any{"name": "bob"}, &u)
		if !strings.HasPrefix(u.SubURL, "https://vpn.example.com:2096/sub/") {
			t.Fatal(u.SubURL)
		}
	})
}
