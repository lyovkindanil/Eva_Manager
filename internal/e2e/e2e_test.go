// Package e2e — сквозные тесты панели: настоящий Xray с конфигом панели
// и настоящий клиент, подключающийся по ссылке панели.
package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	vpnparser "github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/profile"
	xraypanel "github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// TestLinkToConnection — главный сквозной тест этапа 1: ссылка,
// сгенерированная панелью из той же модели, что и конфиг ядра,
// разбирается парсером VPN4Eva, и по ней настоящий клиент подключается
// к настоящему Xray с конфигом панели и прогоняет трафик. Трафик виден
// в статистике ядра под email = id пользователя.
//
// Клиентом выступает второй процесс Xray с конфигом, собранным из
// полей разобранной ссылки. Официальный sing-box (движок VPN4Eva) на
// такой сервер не поднимется намеренно: он вырезает гибридный ключ
// X25519MLKEM768 из ClientHello (common/tls/reality_client.go), а
// Xray-core 26.9.8+ без него Reality не подтверждает — поэтому клиент
// и патчит sing-box. Двигатель клиента проверяется его собственным CI;
// здесь проверяется связка «конфиг панели + ссылка панели + парсер
// клиента + настоящее подключение».
func TestLinkToConnection(t *testing.T) {
	xrayBin := os.Getenv(xraypanel.EnvXrayBin)
	if xrayBin == "" {
		t.Skipf("нужен %s", xraypanel.EnvXrayBin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Сайт маскировки: локальный TLS 1.3 сервер. Клиентский SNI —
	// e2e.test (ServerNames), dest указывает на этот сервер.
	dest, destAddr := fakeDest(t, "e2e.test")
	defer dest.Close()

	// Инбаунд панели на свободном порту; ссылки — на 127.0.0.1.
	xrayPort := freePort(t)
	in, err := inbound.NewVLESSReality(store.NewID(), "node", "E2E", xrayPort, "127.0.0.1", destAddr)
	if err != nil {
		t.Fatal(err)
	}
	in.Reality.ServerNames = []string{"e2e.test"}
	apiAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg, err := inbound.BuildConfig(apiAddr, []inbound.Inbound{in})
	if err != nil {
		t.Fatal(err)
	}
	// Цель трафика в тесте — локальный HTTP-сервер; снимаем правило,
	// блокирующее частные сети (в боевом конфиге оно на месте).
	cfg = allowPrivate(t, cfg)

	proc := xraypanel.NewProcess(xrayBin, filepath.Join(t.TempDir(), "xray", "config.json"))
	if err := proc.Start(ctx, cfg); err != nil {
		t.Fatalf("xray: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop() })

	// Пользователь попадает в ядро через API, как делает панель.
	api, err := xraypanel.Dial(apiAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	userID, uuid := store.NewID(), store.NewUUID()
	if err := api.AddVLESSUser(ctx, in.Tag(), userID, uuid); err != nil {
		t.Fatalf("пользователь в ядро: %v", err)
	}

	// Ссылка из модели панели → парсер клиента.
	link, err := in.Link(uuid, "тест · E2E")
	if err != nil {
		t.Fatal(err)
	}
	v, err := vpnparser.ParseVLESS(link)
	if err != nil {
		t.Fatalf("парсер VPN4Eva отверг ссылку панели: %v", err)
	}
	pbk, _ := in.Reality.PublicKey()
	switch {
	case v.UUID != uuid || v.Server != "127.0.0.1" || v.Port != uint16(xrayPort):
		t.Fatalf("адрес ссылки: %+v", v)
	case v.Security != "reality" || v.Flow != "xtls-rprx-vision" || v.Fingerprint != "chrome":
		t.Fatalf("параметры ссылки: %+v", v)
	case v.PublicKey != pbk || v.ShortID != in.Reality.ShortIDs[0] || v.SNI != "e2e.test":
		t.Fatalf("reality в ссылке: %+v", v)
	case !v.MLKEM:
		t.Fatalf("в ссылке нет support-x25519mlkem768: %+v", v)
	}

	// Клиентский Xray: socks-вход и VLESS Reality выход, собранные из
	// полей разобранной ссылки.
	socksPort := freePort(t)
	clientCfg := map[string]any{
		"log": map[string]any{"loglevel": "debug"},
		"inbounds": []any{map[string]any{
			"tag": "socks-in", "listen": "127.0.0.1", "port": socksPort,
			"protocol": "socks", "settings": map[string]any{"udp": false},
		}},
		"outbounds": []any{map[string]any{
			"tag": "proxy", "protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": v.Server, "port": v.Port,
				"users": []any{map[string]any{
					"id": v.UUID, "encryption": "none", "flow": v.Flow,
				}},
			}}},
			"streamSettings": map[string]any{
				"network": "tcp", "security": "reality",
				"realitySettings": map[string]any{
					"serverName":  v.SNI,
					"publicKey":   v.PublicKey,
					"shortId":     v.ShortID,
					"fingerprint": v.Fingerprint,
				},
			},
		}},
	}
	raw, err := json.Marshal(clientCfg)
	if err != nil {
		t.Fatal(err)
	}
	clientProc := xraypanel.NewProcess(xrayBin, filepath.Join(t.TempDir(), "xray-client", "config.json"))
	if err := clientProc.Start(ctx, raw); err != nil {
		t.Fatalf("клиентский xray: %v", err)
	}
	t.Cleanup(func() { _ = clientProc.Stop() })

	// Трафик через туннель: HTTP-запрос из теста → socks → VLESS
	// Reality → Xray панели → локальный «интернет».
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("eva-e2e-ok"))
	}))
	defer target.Close()
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "socks5", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort))})},
		Timeout:   20 * time.Second,
	}
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("запрос через туннель: %v\nxray сервера:\n%s\nxray клиента:\n%s", err, proc.Logs(), clientProc.Logs())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "eva-e2e-ok" {
		t.Fatalf("ответ через туннель: %s %q\nxray сервера:\n%s\nxray клиента:\n%s", resp.Status, body, proc.Logs(), clientProc.Logs())
	}

	// Трафик посчитан ядром под email = id пользователя.
	traffic, err := api.UserTraffic(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if tt := traffic[userID]; tt.Uplink+tt.Downlink == 0 {
		t.Errorf("трафик пользователя не посчитан: %+v (весь: %+v)", tt, traffic)
	}
}

// fakeDest поднимает TLS 1.3 сервер — «сайт маскировки» для Reality —
// с самоподписанным сертификатом на имя name. Xray проксирует в него
// TLS-рукопожатия; действительность сертификата никто не проверяет.
func fakeDest(t *testing.T, name string) (net.Listener, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		DNSNames:              []string{name},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h2", "http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				_, _ = io.ReadAll(io.LimitReader(c, 64<<10))
			}()
		}
	}()
	return ln, ln.Addr().String()
}

// allowPrivate разрешает локальную цель трафика: убирает из конфига
// панели правило блокировки частных сетей и добавляет в freedom
// finalRules с allow для 127.0.0.1 — Xray-core 26.7.11+ блокирует
// частные адреса и на уровне freedom (defaultBlockPrivateRule).
func allowPrivate(t *testing.T, cfg []byte) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
	m["routing"] = map[string]any{"rules": []any{}}
	m["log"] = map[string]any{"loglevel": "debug"} // печатается только при падении
	for _, o := range m["outbounds"].([]any) {
		ob := o.(map[string]any)
		if ob["tag"] == "direct" {
			ob["settings"] = map[string]any{"finalRules": []any{
				map[string]any{"action": "allow", "network": "tcp", "ip": []any{"127.0.0.1/32"}},
			}}
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
