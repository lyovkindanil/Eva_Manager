package inbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	vpnparser "github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/profile"
	"github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/vlessenc"
	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

const testUUID = "0194b6e0-7a1d-7b8c-9d2e-3f4a5b6c7d8e"

func newTestInbound(t *testing.T, withEncryption bool) Inbound {
	t.Helper()
	in, err := NewVLESSReality("id1", "node1", "NL-1", 443, "vpn.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if withEncryption {
		if err := in.EnableEncryption("native", "0rtt", 600, "100-111-1111.75-0-111.50-0-3333"); err != nil {
			t.Fatal(err)
		}
	}
	return in
}

func TestNewVLESSRealityDefaults(t *testing.T) {
	in := newTestInbound(t, false)
	r := in.Reality
	if r.Dest != DefaultDests[0] || len(r.ServerNames) != 1 || r.ServerNames[0] != "www.microsoft.com" {
		t.Errorf("маскировка: %+v", r)
	}
	if r.Fingerprint != "chrome" || !r.MLKEM || r.Flow != "xtls-rprx-vision" {
		t.Errorf("умолчания: %+v", r)
	}
	pub, err := r.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(pub)
	if len(raw) != 32 {
		t.Errorf("публичный ключ %d байт, ждали 32", len(raw))
	}
	if len(r.ShortIDs) != 1 || len(r.ShortIDs[0]) != 16 {
		t.Errorf("shortIds: %v", r.ShortIDs)
	}
}

// Ссылка панели разбирается парсером самого VPN4Eva и совпадает с моделью
// инбаунда по всем полям — главный тест против «неполных ссылок» 3x-ui.
func TestLinkParsedByVPN4Eva(t *testing.T) {
	for _, withEnc := range []bool{false, true} {
		in := newTestInbound(t, withEnc)
		link, err := in.Link(testUUID, "Данил · NL-1")
		if err != nil {
			t.Fatal(err)
		}
		v, err := vpnparser.ParseVLESS(link)
		if err != nil {
			t.Fatalf("парсер VPN4Eva не принял ссылку: %v\n%s", err, link)
		}
		r := in.Reality
		pbk, _ := r.PublicKey()
		switch {
		case v.UUID != testUUID:
			t.Errorf("uuid = %q", v.UUID)
		case v.Server != "vpn.example.com" || v.Port != 443:
			t.Errorf("адрес = %s:%d", v.Server, v.Port)
		case v.Name != "Данил · NL-1":
			t.Errorf("имя = %q", v.Name)
		case v.Network != "tcp" || v.Security != "reality":
			t.Errorf("type=%q security=%q", v.Network, v.Security)
		case v.PublicKey != pbk:
			t.Errorf("pbk = %q, ждали %q", v.PublicKey, pbk)
		case v.ShortID != r.ShortIDs[0]:
			t.Errorf("sid = %q", v.ShortID)
		case v.SNI != r.ServerNames[0]:
			t.Errorf("sni = %q", v.SNI)
		case v.Fingerprint != "chrome":
			t.Errorf("fp = %q", v.Fingerprint)
		case v.Flow != "xtls-rprx-vision":
			t.Errorf("flow = %q", v.Flow)
		case !v.MLKEM:
			t.Error("флаг support-x25519mlkem768 потерян")
		}
		enc, _ := in.encryptionString()
		if !withEnc {
			if v.Encryption != "" {
				t.Errorf("encryption = %q, ждали выключено", v.Encryption)
			}
			continue
		}
		if v.Encryption != enc {
			t.Fatalf("encryption = %q, ждали %q", v.Encryption, enc)
		}
		// Строку шифрования принимает и валидатор клиента, и ключ в ней —
		// публичный ML-KEM-768 из seed сервера.
		ec, err := vlessenc.ParseClient(v.Encryption)
		if err != nil {
			t.Fatal(err)
		}
		wantKey, _ := r.Encryption.ClientKey()
		wantRaw, _ := base64.RawURLEncoding.DecodeString(wantKey)
		if len(ec.Keys) != 1 || string(ec.Keys[0]) != string(wantRaw) {
			t.Errorf("ключ в ссылке не совпал с ключом сервера")
		}
		if ec.Padding != r.Encryption.Padding {
			t.Errorf("padding = %q, ждали %q", ec.Padding, r.Encryption.Padding)
		}
	}
}

// Строка decryption для конфига Xray разбирается по правилам Xray-core
// (infra/conf/vless.go) и несёт приватный ключ ML-KEM-768.
func TestDecryptionStringMatchesXrayRules(t *testing.T) {
	in := newTestInbound(t, true)
	e := in.Reality.Encryption
	s := in.decryptionString()
	parts := strings.Split(s, ".")
	if len(parts) < 4 || parts[0] != "mlkem768x25519plus" || parts[1] != "native" {
		t.Fatalf("decryption = %q", s)
	}
	sec, err := strconv.Atoi(strings.TrimSuffix(parts[2], "s"))
	if err != nil || sec != e.Seconds {
		t.Errorf("seconds = %q", parts[2])
	}
	var padding, keys []string
	for _, p := range parts[3:] {
		if len(p) < 20 {
			padding = append(padding, p)
			continue
		}
		raw, _ := base64.RawURLEncoding.DecodeString(p)
		if len(raw) != 32 && len(raw) != 64 {
			t.Errorf("ключ %d байт, Xray ждёт 32 или 64", len(raw))
		}
		keys = append(keys, p)
	}
	if strings.Join(padding, ".") != e.Padding {
		t.Errorf("padding = %q, ждали %q", padding, e.Padding)
	}
	if len(keys) != 1 || keys[0] != e.Seed {
		t.Errorf("seed в decryption не совпал")
	}

	// Без шифрования Xray требует явного "none".
	in2 := newTestInbound(t, false)
	if got := in2.decryptionString(); got != "none" {
		t.Errorf("decryption = %q, ждали none", got)
	}
}

func TestConfigFragmentShape(t *testing.T) {
	in := newTestInbound(t, true)
	frag, err := in.ConfigFragment()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(frag)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tag      string `json:"tag"`
		Listen   string `json:"listen"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Settings struct {
			Clients    []any  `json:"clients"`
			Decryption string `json:"decryption"`
		} `json:"settings"`
		StreamSettings struct {
			Network         string `json:"network"`
			Security        string `json:"security"`
			RealitySettings struct {
				Dest        string   `json:"dest"`
				ServerNames []string `json:"serverNames"`
				PrivateKey  string   `json:"privateKey"`
				ShortIDs    []string `json:"shortIds"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Tag != "in-id1" || got.Port != 443 || got.Protocol != "vless" || got.Listen != "0.0.0.0" {
		t.Errorf("фрагмент: %s", raw)
	}
	if got.Settings.Decryption != in.decryptionString() {
		t.Errorf("decryption = %q", got.Settings.Decryption)
	}
	rs := got.StreamSettings.RealitySettings
	if got.StreamSettings.Network != "tcp" || got.StreamSettings.Security != "reality" ||
		rs.Dest != in.Reality.Dest || rs.PrivateKey != in.Reality.PrivateKey ||
		len(rs.ServerNames) != 1 || len(rs.ShortIDs) != 1 {
		t.Errorf("realitySettings: %s", raw)
	}
	if len(got.Settings.Clients) != 0 {
		t.Errorf("clients должны добавляться через API, не через конфиг")
	}
}

func TestBuildConfig(t *testing.T) {
	on := newTestInbound(t, true)
	off := newTestInbound(t, false)
	off.ID, off.Name, off.Enabled, off.Port = "id2", "выключен", false, 8443
	cfg, err := BuildConfig("127.0.0.1:10085", []Inbound{on, off})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Inbounds []struct {
			Tag  string `json:"tag"`
			Port int    `json:"port"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Inbounds) != 1 || parsed.Inbounds[0].Tag != "in-id1" {
		t.Errorf("в конфиг попали не те инбаунды: %+v", parsed.Inbounds)
	}
}

// Конфиг с инбаундами принимает настоящий Xray (в CI — всегда, локально —
// при EVA_TEST_XRAY_BIN).
func TestBuildConfigAcceptedByXray(t *testing.T) {
	bin := os.Getenv(xray.EnvXrayBin)
	if bin == "" {
		t.Skipf("не задан %s", xray.EnvXrayBin)
	}
	plain := newTestInbound(t, false)
	plain.ID, plain.Port = "plain", 20443
	enc := newTestInbound(t, true)
	enc.ID, enc.Port = "enc", 20444
	cfg, err := BuildConfig("127.0.0.1:10085", []Inbound{plain, enc})
	if err != nil {
		t.Fatal(err)
	}
	if err := xray.Test(context.Background(), bin, cfg); err != nil {
		t.Fatalf("Xray отверг конфиг: %v\n%s", err, cfg)
	}
}

func TestValidate(t *testing.T) {
	good := newTestInbound(t, true)
	cases := []struct {
		name string
		mut  func(*Inbound)
	}{
		{"протокол", func(in *Inbound) { in.Protocol = "trojan" }},
		{"порт 0", func(in *Inbound) { in.Port = 0 }},
		{"порт 70000", func(in *Inbound) { in.Port = 70000 }},
		{"dest без порта", func(in *Inbound) { in.Reality.Dest = "example.com" }},
		{"dest с плохим портом", func(in *Inbound) { in.Reality.Dest = "example.com:abc" }},
		{"без serverNames", func(in *Inbound) { in.Reality.ServerNames = nil }},
		{"serverName с пробелом", func(in *Inbound) { in.Reality.ServerNames = []string{"ex ample.com"} }},
		{"плохой ключ", func(in *Inbound) { in.Reality.PrivateKey = "не base64!!!" }},
		{"без shortIds", func(in *Inbound) { in.Reality.ShortIDs = nil }},
		{"shortId не hex", func(in *Inbound) { in.Reality.ShortIDs = []string{"zz"} }},
		{"shortId нечётный", func(in *Inbound) { in.Reality.ShortIDs = []string{"abc"} }},
		{"без fp", func(in *Inbound) { in.Reality.Fingerprint = "" }},
		{"другой flow", func(in *Inbound) { in.Reality.Flow = "none" }},
		{"режим шифрования", func(in *Inbound) { in.Reality.Encryption.Mode = "aes" }},
		{"rtt", func(in *Inbound) { in.Reality.Encryption.RTT = "2rtt" }},
		{"0rtt без срока", func(in *Inbound) { in.Reality.Encryption.Seconds = 0 }},
		{"плохой паддинг", func(in *Inbound) { in.Reality.Encryption.Padding = "много" }},
		{"плохой seed", func(in *Inbound) { in.Reality.Encryption.Seed = "bm8=" }},
	}
	for _, tc := range cases {
		in := good
		enc := *in.Reality.Encryption
		in.Reality.Encryption = &enc
		tc.mut(&in)
		if err := in.Validate(); err == nil {
			t.Errorf("%s: принят", tc.name)
		}
		if _, err := in.ConfigFragment(); err == nil {
			t.Errorf("%s: фрагмент построился", tc.name)
		}
	}
	if err := good.Validate(); err != nil {
		t.Errorf("хороший инбаунд отвергнут: %v", err)
	}
}

func TestNextPort(t *testing.T) {
	busy := func(p int) bool { return p == 443 || p == 2053 }
	if p := NextPort(nil, nil); p != 443 {
		t.Errorf("пусто: %d", p)
	}
	if p := NextPort(nil, busy); p != 8443 {
		t.Errorf("443 занят: %d", p)
	}
	if p := NextPort([]Inbound{{Port: 8443}}, busy); p != 2083 {
		t.Errorf("443, 2053 заняты, 8443 у инбаунда: %d", p)
	}
}
