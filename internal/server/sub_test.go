package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	vpnparser "github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/profile"
)

// Подписка: base64 от ссылок построчно, заголовки profile-title /
// subscription-userinfo / profile-update-interval в форме, которую
// читает VPN4Eva; каждая ссылка разбирается парсером клиента.
func TestSubscription(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		ts := newServer(t, d, builtWeb)
		st := store.New(d)
		node, err := st.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		inIDs := []string{}
		for i, name := range []string{"NL-1", "DE-1"} {
			in, err := inbound.NewVLESSReality(store.NewID(), node.ID, name, 443+i, "vpn.example.com", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.CreateInbound(ctx, in); err != nil {
				t.Fatal(err)
			}
			inIDs = append(inIDs, in.ID)
		}
		expires := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
		u, err := st.CreateUser(ctx, node.ID, "Данил", "", 10<<30, expires)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddUserTraffic(ctx, u.ID, time.Now().UTC().Format("2006-01-02"), 100, 200); err != nil {
			t.Fatal(err)
		}

		r, body := get(t, ts.URL+"/sub/"+u.SubToken)
		if r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, body)
		}

		// Кириллическое имя — в base64-форме, интервал и запрет кэша.
		title := r.Header.Get("Profile-Title")
		if !strings.HasPrefix(title, "base64:") {
			t.Fatalf("profile-title: %q", title)
		}
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(title, "base64:")); err != nil || string(dec) != "Данил" {
			t.Errorf("profile-title раскодирован: %q, %v", dec, err)
		}
		wantInfo := fmt.Sprintf("upload=0; download=300; total=%d; expire=%d", int64(10<<30), expires.Unix())
		if got := r.Header.Get("Subscription-Userinfo"); got != wantInfo {
			t.Errorf("subscription-userinfo: %q, ожидался %q", got, wantInfo)
		}
		if got := r.Header.Get("Profile-Update-Interval"); got != "12" {
			t.Errorf("profile-update-interval: %q", got)
		}
		if got := r.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("cache-control: %q", got)
		}

		// Тело — base64 от ссылок построчно; обе разбирает парсер VPN4Eva.
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
		if err != nil {
			t.Fatalf("тело не base64: %v", err)
		}
		links := strings.Split(string(decoded), "\n")
		if len(links) != 2 {
			t.Fatalf("ссылок %d: %q", len(links), decoded)
		}
		for i, link := range links {
			v, err := vpnparser.ParseVLESS(link)
			if err != nil {
				t.Fatalf("парсер клиента отверг ссылку %d: %v", i, err)
			}
			if v.UUID != u.UUID {
				t.Errorf("ссылка %d: uuid %s, ожидался %s", i, v.UUID, u.UUID)
			}
		}
		if !strings.Contains(links[0], "%C2%B7") { // «·» в подписи
			t.Errorf("подпись ссылки: %s", links[0])
		}

		// Несуществующий токен — 404, без намёка на формат токенов.
		if r, body := get(t, ts.URL+"/sub/"+strings.Repeat("0", 32)); r.StatusCode != 404 {
			t.Errorf("чужой токен: %d %s", r.StatusCode, body)
		}

		// Пользователь без инбаундов: 200 и пустая подписка (клиент скажет
		// «в подписке нет серверов»), заголовки на месте.
		lonely, err := st.CreateUser(ctx, node.ID, "bob", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range inIDs {
			if err := st.SetInboundEnabled(ctx, id, false); err != nil {
				t.Fatal(err)
			}
		}
		r, body = get(t, ts.URL+"/sub/"+lonely.SubToken)
		if r.StatusCode != 200 {
			t.Fatalf("без инбаундов: %d %s", r.StatusCode, body)
		}
		if decoded, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(body)); len(decoded) != 0 {
			t.Errorf("подписка без инбаундов не пуста: %q", decoded)
		}
		if got := r.Header.Get("Profile-Title"); got != "bob" {
			t.Errorf("ascii profile-title: %q", got)
		}
	})
}

// AmneziaWG в подписке — ключом vpn://: его разбирает парсер VPN4Eva
// (через ParseServer, как строку подписки), с адресом пира пользователя.
func TestSubscriptionAWG(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		ts := newServer(t, d, builtWeb)
		st := store.New(d)
		node, err := st.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		vless, err := inbound.NewVLESSReality(store.NewID(), node.ID, "NL-1", 443, "vpn.example.com", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CreateInbound(ctx, vless); err != nil {
			t.Fatal(err)
		}
		in, err := awg.New(store.NewID(), node.ID, "NL AWG", "awg0", 0, "vpn.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CreateAWG(ctx, in); err != nil {
			t.Fatal(err)
		}
		u, err := st.CreateUser(ctx, node.ID, "Данил", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		_, body := get(t, ts.URL+"/sub/"+u.SubToken)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
		if err != nil {
			t.Fatal(err)
		}
		links := strings.Split(string(decoded), "\n")
		if len(links) != 2 || !strings.HasPrefix(links[1], "vpn://") {
			t.Fatalf("%q", links)
		}
		for _, l := range links {
			if !vpnparser.IsServerLink(l) {
				t.Fatalf("клиент не считает строку сервером: %s", l)
			}
		}
		s, err := vpnparser.ParseServer(links[1])
		if err != nil {
			t.Fatal(err)
		}
		if s.Protocol != vpnparser.ProtoAmneziaWG || s.Name != "Данил · NL AWG" || s.Host != "vpn.example.com" ||
			s.WireGuard.Address[0].String() != "10.8.0.2/32" {
			t.Fatalf("%+v %+v", s, s.WireGuard)
		}
		// Выключенный интерфейс из подписки пропадает.
		if err := st.SetAWGEnabled(ctx, in.ID, false); err != nil {
			t.Fatal(err)
		}
		_, body = get(t, ts.URL+"/sub/"+u.SubToken)
		decoded, _ = base64.StdEncoding.DecodeString(strings.TrimSpace(body))
		if strings.Contains(string(decoded), "vpn://") {
			t.Fatalf("%q", decoded)
		}
	})
}
