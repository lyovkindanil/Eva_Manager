// Package awg — AmneziaWG на узле: модель интерфейса (ключи, подсеть,
// маскировка), из которой строятся и настройки сервера (UAPI
// amneziawg-go), и конфигурация клиента (.conf и ключ vpn://). Как и с
// инбаундами Xray, обе стороны берутся из одной модели и не расходятся.
package awg

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Значения по умолчанию.
const (
	DefaultPort   = 51820
	DefaultSubnet = "10.8.0.0/24"
	DefaultMTU    = 1376 // как у AmneziaVPN: с запасом на мусор и S1–S4
	DefaultDNS    = "1.1.1.1, 1.0.0.1"
	// Keepalive клиента: держит запись NAT у провайдера открытой.
	DefaultKeepalive = 25
)

// Interface — сервер AmneziaWG на узле: один сетевой интерфейс
// (awg0, awg1…) и его клиенты-пиры.
type Interface struct {
	ID     string
	NodeID string
	Name   string // подпись для клиентов, например «NL-AWG»
	// Iface — имя сетевого интерфейса на сервере.
	Iface string
	Port  int
	// LinkHost — адрес сервера в конфигурации клиента (Endpoint).
	LinkHost string
	Enabled  bool
	Settings Settings
}

// Settings — параметры интерфейса (JSON в колонке settings).
type Settings struct {
	// PrivateKey — ключ сервера, base64 (как в wg-quick).
	PrivateKey string `json:"private_key"`
	// Subnet — подсеть клиентов; первый адрес — сервер.
	Subnet string `json:"subnet"`
	MTU    int    `json:"mtu"`
	// DNS — для клиентов, через запятую.
	DNS string      `json:"dns"`
	Obf Obfuscation `json:"obfuscation"`
}

// Obfuscation — маскировка AmneziaWG 2.0 (её понимают amneziawg-go,
// приложение AmneziaVPN и VPN4Eva). Нули и пустые строки — не задано.
type Obfuscation struct {
	Jc   int `json:"jc"`
	Jmin int `json:"jmin"`
	Jmax int `json:"jmax"`
	S1   int `json:"s1"`
	S2   int `json:"s2"`
	S3   int `json:"s3"`
	S4   int `json:"s4"`
	// H1–H4 — свои типы сообщений: число или диапазон «a-b».
	H1 string `json:"h1"`
	H2 string `json:"h2"`
	H3 string `json:"h3"`
	H4 string `json:"h4"`
	// I1 — сигнатурный пакет перед рукопожатием, «<b 0x…><r 16>…».
	I1 string `json:"i1,omitempty"`
}

// Peer — клиент интерфейса: пользователь со своим ключом и адресом.
// Приватный ключ клиента хранит панель — из него собирается его
// конфигурация, как у AmneziaVPN и 3x-ui.
type Peer struct {
	InterfaceID  string
	UserID       string
	PrivateKey   string // base64
	PresharedKey string // base64
	Address      netip.Addr
	// PublicKeyB64 — публичный ключ без приватного: так задаётся пир
	// только для удаления (по счётчикам UAPI).
	PublicKeyB64 string
}

// NewKey — ключ X25519 в base64 (как `awg genkey`).
func NewKey() (string, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), nil
}

// NewPSK — общий ключ пира, 32 случайных байта в base64.
func NewPSK() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// PublicKey — публичный ключ для приватного в base64.
func PublicKey(private string) (string, error) {
	b, err := decodeKey(private)
	if err != nil {
		return "", err
	}
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

func decodeKey(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, errors.New("ключ: ожидалось 32 байта в base64")
	}
	return b, nil
}

// hexKey — ключ в hex для UAPI.
func hexKey(s string) (string, error) {
	b, err := decodeKey(s)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func rnd(lo, hi int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(hi-lo+1)))
	if err != nil {
		panic(err)
	}
	return lo + int(n.Int64())
}

// NewObfuscation — случайная маскировка AmneziaWG 2.0 в пределах, которые
// выбирает приложение AmneziaVPN: мусор перед рукопожатием, сдвиги S1–S4 и
// непересекающиеся диапазоны H1–H4 вместо типов 1–4 обычного WireGuard.
func NewObfuscation() Obfuscation {
	o := Obfuscation{
		Jc: rnd(4, 8), Jmin: rnd(40, 64),
		S1: rnd(15, 150), S3: rnd(8, 64), S4: rnd(4, 32),
	}
	o.Jmax = o.Jmin + rnd(40, 300)
	// S2 ≠ S1+56: иначе пакет рукопожатия и ответа одной длины.
	for {
		o.S2 = rnd(15, 150)
		if o.S1+56 != o.S2 {
			break
		}
	}
	// Четыре непересекающихся диапазона в разных четвертях uint32 выше 4.
	quarter := uint64(1) << 30
	hs := [4]*string{&o.H1, &o.H2, &o.H3, &o.H4}
	order := []int{0, 1, 2, 3}
	for i := len(order) - 1; i > 0; i-- {
		j := rnd(0, i)
		order[i], order[j] = order[j], order[i]
	}
	for i, q := range order {
		base := uint64(q)*quarter + 5
		lo := base + uint64(rnd(0, 1<<29))
		hi := lo + uint64(rnd(64, 1<<16))
		*hs[i] = fmt.Sprintf("%d-%d", lo, hi)
	}
	return o
}

// New — интерфейс с новыми ключами и маскировкой.
func New(id, nodeID, name, iface string, port int, linkHost string) (Interface, error) {
	key, err := NewKey()
	if err != nil {
		return Interface{}, err
	}
	if port == 0 {
		port = DefaultPort
	}
	in := Interface{
		ID: id, NodeID: nodeID, Name: name, Iface: iface, Port: port, LinkHost: linkHost, Enabled: true,
		Settings: Settings{PrivateKey: key, Subnet: DefaultSubnet, MTU: DefaultMTU, DNS: DefaultDNS, Obf: NewObfuscation()},
	}
	return in, in.Validate()
}

var ifaceRe = regexp.MustCompile(`^[a-z][a-z0-9]{0,14}$`)

// Validate проверяет всё, что попадёт на сервер и в конфигурацию клиента.
func (in Interface) Validate() error {
	var errs []error
	if strings.TrimSpace(in.Name) == "" {
		errs = append(errs, errors.New("не задано имя"))
	}
	if !ifaceRe.MatchString(in.Iface) {
		errs = append(errs, fmt.Errorf("имя интерфейса %q: латиница и цифры, до 15 символов", in.Iface))
	}
	if in.Port < 1 || in.Port > 65535 {
		errs = append(errs, fmt.Errorf("порт %d вне 1–65535", in.Port))
	}
	if in.LinkHost == "" || strings.ContainsAny(in.LinkHost, " /@?#") {
		errs = append(errs, fmt.Errorf("адрес сервера для клиентов %q: ожидался IP или домен", in.LinkHost))
	}
	s := in.Settings
	if _, err := decodeKey(s.PrivateKey); err != nil {
		errs = append(errs, fmt.Errorf("приватный ключ сервера: %w", err))
	}
	if p, err := netip.ParsePrefix(s.Subnet); err != nil || !p.Addr().Is4() || p.Bits() > 30 || p.Bits() < 16 || p.Masked() != p {
		errs = append(errs, fmt.Errorf("подсеть %q: ожидалась IPv4 от /16 до /30, например %s", s.Subnet, DefaultSubnet))
	}
	if s.MTU < 1280 || s.MTU > 1500 {
		errs = append(errs, fmt.Errorf("MTU %d вне 1280–1500", s.MTU))
	}
	for _, d := range splitList(s.DNS) {
		if net.ParseIP(d) == nil {
			errs = append(errs, fmt.Errorf("DNS %q: ожидался IP", d))
		}
	}
	if err := s.Obf.Validate(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("AmneziaWG %q: %w", in.Name, errors.Join(errs...))
	}
	return nil
}

// Validate — те же правила, что у клиента VPN4Eva (profile.Amnezia).
func (o Obfuscation) Validate() error {
	for _, v := range []struct {
		name string
		val  int
	}{{"Jc", o.Jc}, {"Jmin", o.Jmin}, {"Jmax", o.Jmax}, {"S1", o.S1}, {"S2", o.S2}, {"S3", o.S3}, {"S4", o.S4}} {
		if v.val < 0 {
			return fmt.Errorf("%s не может быть отрицательным", v.name)
		}
	}
	if o.Jc > 128 {
		return fmt.Errorf("Jc %d: не больше 128", o.Jc)
	}
	if o.Jmax < o.Jmin || o.Jmax > 1280 {
		return fmt.Errorf("Jmin %d и Jmax %d: нужно Jmin ≤ Jmax ≤ 1280", o.Jmin, o.Jmax)
	}
	if o.Jc > 0 && o.Jmax == 0 {
		return errors.New("при Jc нужен Jmax")
	}
	if o.S1+56 == o.S2 {
		return errors.New("S2 не может быть равен S1 + 56: рукопожатие и ответ станут одной длины")
	}
	var ranges [4][2]uint64
	for i, h := range []string{o.H1, o.H2, o.H3, o.H4} {
		ranges[i] = [2]uint64{uint64(i + 1), uint64(i + 1)}
		if h == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(h, "-")
		var err error
		if ranges[i][0], err = strconv.ParseUint(lo, 10, 32); err != nil {
			return fmt.Errorf("H%d: ожидалось число или диапазон «a-b», получено %q", i+1, h)
		}
		ranges[i][1] = ranges[i][0]
		if isRange {
			if ranges[i][1], err = strconv.ParseUint(hi, 10, 32); err != nil || ranges[i][1] < ranges[i][0] {
				return fmt.Errorf("H%d: некорректный диапазон %q", i+1, h)
			}
		}
	}
	for i := range ranges {
		for j := i + 1; j < len(ranges); j++ {
			if ranges[i][0] <= ranges[j][1] && ranges[j][0] <= ranges[i][1] {
				return fmt.Errorf("H%d и H%d пересекаются", i+1, j+1)
			}
		}
	}
	if o.I1 != "" && (!strings.HasPrefix(o.I1, "<") || !strings.HasSuffix(o.I1, ">")) {
		return errors.New("I1: ожидались теги вида <b 0x…><r 16>")
	}
	return nil
}

// ServerAddr — адрес сервера в подсети клиентов (первый).
func (in Interface) ServerAddr() netip.Prefix {
	p := netip.MustParsePrefix(in.Settings.Subnet)
	return netip.PrefixFrom(p.Addr().Next(), p.Bits())
}

// FreeAddr — свободный адрес клиента: первый после сервера, которого нет
// в used. ok = false — подсеть кончилась.
func (in Interface) FreeAddr(used map[netip.Addr]bool) (netip.Addr, bool) {
	p := netip.MustParsePrefix(in.Settings.Subnet)
	a := in.ServerAddr().Addr().Next()
	for ; p.Contains(a); a = a.Next() {
		if !a.Next().IsValid() || !p.Contains(a.Next()) {
			break // широковещательный
		}
		if !used[a] {
			return a, true
		}
	}
	return netip.Addr{}, false
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func hexToB64(h string) string {
	b, err := hex.DecodeString(h)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

// PublicKey — публичный ключ клиента.
func (p Peer) PublicKey() (string, error) {
	if p.PrivateKey == "" && p.PublicKeyB64 != "" {
		return p.PublicKeyB64, nil
	}
	return PublicKey(p.PrivateKey)
}
