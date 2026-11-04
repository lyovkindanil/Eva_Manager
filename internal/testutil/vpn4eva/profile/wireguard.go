// Сгенерировано scripts/sync-vpn4eva-parser.sh — не править вручную.
// Источник: github.com/lyovkindanil/Vpn4Eva, коммит 7a5ab396a8731568ccf22b736898b8fcebca3b7a:
//   core/internal/profile/wireguard.go

package profile

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// WireGuard — клиентская конфигурация WireGuard с одним пиром.
// С Amnezia это AmneziaWG: те же ключи, плюс маскировка пакетов.
type WireGuard struct {
	PrivateKey string // base64
	Address    []netip.Prefix
	MTU        uint32 // 0 — по умолчанию ядра
	// Пир (сервер).
	PublicKey    string // base64
	PresharedKey string // base64; пусто — нет
	AllowedIPs   []netip.Prefix
	Keepalive    uint16 // секунды; 0 — выключено
	// Reserved — три байта в заголовке пакета (Cloudflare WARP); пусто — нули.
	Reserved []uint8
	Amnezia  *Amnezia
	// NoJunk — в .conf явно Jc = 0: обычному WireGuard не слать мусорные
	// пакеты против DPI, которые клиент иначе добавляет сам.
	NoJunk bool
}

// Amnezia — параметры маскировки AmneziaWG (версии 1.0, 1.5, 2.0 и 3.1).
// Нули и пустые строки — параметр не задан.
type Amnezia struct {
	// Jc мусорных пакетов размером от Jmin до Jmax перед рукопожатием.
	Jc, Jmin, Jmax int
	// S1–S4: случайные байты перед сообщениями рукопожатия, ответа,
	// cookie и данных.
	S1, S2, S3, S4 int
	// H1–H4: свои типы сообщений вместо 1–4; число или диапазон "a-b".
	H1, H2, H3, H4 string
	// I1–I5: сигнатурные пакеты перед рукопожатием (AWG 1.5), "<b 0x…><r 16>…".
	I1, I2, I3, I4, I5 string

	// AWG 3.1.
	// HeaderProtectionKey — ключ шифрования заголовков (base64, 32 байта);
	// требует S1–S4 не меньше 12.
	HeaderProtectionKey string
	// ContentPaddingAddition — сколько нулей добавлять к данным, «a-b».
	ContentPaddingAddition string
	// RandomTrailers — случайные хвосты после пакетов; DisableCookies — не
	// отвечать cookie на рукопожатия.
	RandomTrailers, DisableCookies bool
	// Случайные таймеры протокола, «a-b» (секунды или число попыток).
	RekeyAfterTime, RekeyTimeout, RejectAfterTime, KeepaliveTimeout, MaxHandshakeAttempts string
}

// isWireGuardConf — текст похож на файл конфигурации wg-quick.
func isWireGuardConf(s string) bool {
	return strings.Contains(s, "[Interface]") || strings.Contains(s, "[interface]")
}

// parseWireGuardConf разбирает файл конфигурации WireGuard или AmneziaWG
// в формате wg-quick. Ключи wg-quick, которые клиенту не нужны (DNS, Table,
// PostUp…), пропускаются: DNS и маршруты задаёт Vpn4Eva.
func parseWireGuardConf(text string) (*Server, error) {
	const proto = "wireguard"
	w := &WireGuard{}
	a := &Amnezia{}
	var endpoint string
	section, peers := "", 0
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			switch section {
			case "interface":
			case "peer":
				peers++
				if peers > 1 {
					return nil, fmt.Errorf("%s: несколько [Peer] пока не поддерживается", proto)
				}
			default:
				return nil, fmt.Errorf("%s: строка %d: неизвестная секция [%s]", proto, n, section)
			}
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s: строка %d: ожидалось «ключ = значение»", proto, n)
		}
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		// Значение I1–I5 может содержать «#» внутри <…>, комментарий
		// после значения встречается только у простых ключей.
		if !strings.HasPrefix(val, "<") {
			if i := strings.Index(val, "#"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
		}
		var err error
		switch section {
		case "interface":
			err = w.setInterface(a, key, val)
		case "peer":
			if key == "endpoint" {
				endpoint = val
			} else {
				err = w.setPeer(key, val)
			}
		default:
			err = fmt.Errorf("ключ вне секции")
		}
		if err != nil {
			return nil, fmt.Errorf("%s: строка %d: %s: %w", proto, n, key, err)
		}
	}
	if peers == 0 {
		return nil, fmt.Errorf("%s: в конфигурации нет секции [Peer]", proto)
	}
	if endpoint == "" {
		return nil, fmt.Errorf("%s: у [Peer] нет Endpoint — адреса сервера", proto)
	}
	host, port, err := splitHostPort(endpoint, proto)
	if err != nil {
		return nil, err
	}
	if *a != (Amnezia{}) {
		if err := a.validate(); err != nil {
			return nil, fmt.Errorf("amneziawg: %w", err)
		}
		w.Amnezia = a
	}
	return w.server("", host, port)
}

func (w *WireGuard) setInterface(a *Amnezia, key, val string) error {
	var err error
	switch key {
	case "privatekey":
		w.PrivateKey = val
	case "address":
		w.Address, err = parsePrefixes(val)
	case "mtu":
		var v uint64
		v, err = strconv.ParseUint(val, 10, 32)
		w.MTU = uint32(v)
	case "jc":
		a.Jc, err = strconv.Atoi(val)
		w.NoJunk = err == nil && a.Jc == 0
	case "jmin":
		a.Jmin, err = strconv.Atoi(val)
	case "jmax":
		a.Jmax, err = strconv.Atoi(val)
	case "s1":
		a.S1, err = strconv.Atoi(val)
	case "s2":
		a.S2, err = strconv.Atoi(val)
	case "s3":
		a.S3, err = strconv.Atoi(val)
	case "s4":
		a.S4, err = strconv.Atoi(val)
	case "h1":
		a.H1 = val
	case "h2":
		a.H2 = val
	case "h3":
		a.H3 = val
	case "h4":
		a.H4 = val
	case "i1":
		a.I1 = val
	case "i2":
		a.I2 = val
	case "i3":
		a.I3 = val
	case "i4":
		a.I4 = val
	case "i5":
		a.I5 = val
	case "headerprotectionkey":
		a.HeaderProtectionKey = val
	case "contentpaddingaddition":
		a.ContentPaddingAddition, err = normRange(val)
	case "randomtrailers":
		a.RandomTrailers, err = parseOnOff(val)
	case "disablecookies":
		a.DisableCookies, err = parseOnOff(val)
	case "rekeyaftertime":
		a.RekeyAfterTime, err = normRange(val)
	case "rekeytimeout":
		a.RekeyTimeout, err = normRange(val)
	case "rejectaftertime":
		a.RejectAfterTime, err = normRange(val)
	case "keepalivetimeout":
		a.KeepaliveTimeout, err = normRange(val)
	case "maxhandshakeattempts":
		a.MaxHandshakeAttempts, err = normRange(val)
	}
	// DNS, ListenPort, Table, PostUp и прочее клиенту не нужны.
	return err
}

func (w *WireGuard) setPeer(key, val string) error {
	var err error
	switch key {
	case "publickey":
		w.PublicKey = val
	case "presharedkey":
		w.PresharedKey = val
	case "allowedips":
		w.AllowedIPs, err = parsePrefixes(val)
	case "persistentkeepalive":
		if val == "off" {
			return nil
		}
		var v uint64
		v, err = strconv.ParseUint(val, 10, 16)
		w.Keepalive = uint16(v)
	}
	return err
}

// parseWireGuardURL разбирает ссылку в формате v2rayN/Xray:
// wireguard://PRIVATE_KEY@host:port?publickey=…&address=…&mtu=…&reserved=1,2,3#name
// Принимаются и названия параметров из других клиентов (peer_pk, local_address…).
func parseWireGuardURL(raw string) (*Server, error) {
	const proto = "wireguard"
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: некорректная ссылка: %w", proto, err)
	}
	host, port, err := splitHostPort(u.Host, proto)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := q.Get(k); v != "" {
				return v
			}
		}
		return ""
	}
	w := &WireGuard{
		PrivateKey:   get("privatekey", "private_key", "secretkey", "pk"),
		PublicKey:    get("publickey", "public_key", "peer_pk", "peer_public_key"),
		PresharedKey: get("presharedkey", "pre_shared_key", "preshared_key", "psk"),
	}
	if u.User != nil && u.User.Username() != "" {
		w.PrivateKey = u.User.Username()
	}
	if v := get("address", "local_address", "ip"); v != "" {
		if w.Address, err = parsePrefixes(v); err != nil {
			return nil, fmt.Errorf("%s: address: %w", proto, err)
		}
	}
	if v := get("allowedips", "allowed_ips"); v != "" {
		if w.AllowedIPs, err = parsePrefixes(v); err != nil {
			return nil, fmt.Errorf("%s: allowedips: %w", proto, err)
		}
	}
	if v := get("mtu"); v != "" {
		m, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%s: mtu %q", proto, v)
		}
		w.MTU = uint32(m)
	}
	if v := get("keepalive", "persistentkeepalive", "persistent_keepalive"); v != "" {
		k, err := strconv.ParseUint(v, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("%s: keepalive %q", proto, v)
		}
		w.Keepalive = uint16(k)
	}
	if v := get("reserved"); v != "" {
		if w.Reserved, err = parseReserved(v); err != nil {
			return nil, fmt.Errorf("%s: reserved: %w", proto, err)
		}
	}
	return w.server(u.Fragment, host, port)
}

// server проверяет ключи, подставляет значения по умолчанию и собирает Server.
func (w *WireGuard) server(name, host string, port uint16) (*Server, error) {
	proto := ProtoWireGuard
	if w.Amnezia != nil {
		proto = ProtoAmneziaWG
	}
	// В ссылках ключ часто экранирован не полностью: «+» превращается в пробел.
	w.PrivateKey = strings.ReplaceAll(w.PrivateKey, " ", "+")
	w.PublicKey = strings.ReplaceAll(w.PublicKey, " ", "+")
	w.PresharedKey = strings.ReplaceAll(w.PresharedKey, " ", "+")
	if err := checkKey(w.PrivateKey); err != nil {
		return nil, fmt.Errorf("%s: закрытый ключ (PrivateKey): %w", proto, err)
	}
	if err := checkKey(w.PublicKey); err != nil {
		return nil, fmt.Errorf("%s: открытый ключ сервера (PublicKey): %w", proto, err)
	}
	if w.PresharedKey != "" {
		if err := checkKey(w.PresharedKey); err != nil {
			return nil, fmt.Errorf("%s: PresharedKey: %w", proto, err)
		}
	}
	if len(w.Address) == 0 {
		return nil, fmt.Errorf("%s: не указан адрес в туннеле (Address)", proto)
	}
	if len(w.AllowedIPs) == 0 {
		w.AllowedIPs = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	}
	return &Server{Protocol: proto, Name: name, Host: host, Port: port, WireGuard: w}, nil
}

// HasIPv6 — в туннеле есть адрес IPv6.
func (w *WireGuard) HasIPv6() bool {
	for _, p := range w.Address {
		if p.Addr().Is6() && !p.Addr().Is4In6() {
			return true
		}
	}
	return false
}

func checkKey(k string) error {
	if k == "" {
		return fmt.Errorf("не указан")
	}
	b, err := base64.StdEncoding.DecodeString(k)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("ожидалось 32 байта в base64")
	}
	return nil
}

// parsePrefixes — список через запятую; одиночный адрес становится /32 или /128.
func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		p, err := parsePrefix(f)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

var reservedSep = regexp.MustCompile(`[,\s]+`)

// parseReserved — три байта: "1,2,3" или base64 из трёх байт.
func parseReserved(s string) ([]uint8, error) {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 3 {
		return b, nil
	}
	var out []uint8
	for _, f := range reservedSep.Split(s, -1) {
		if f == "" {
			continue
		}
		v, err := strconv.ParseUint(f, 10, 8)
		if err != nil {
			return nil, fmt.Errorf("%q: ожидалось число 0–255", f)
		}
		out = append(out, uint8(v))
	}
	if len(out) != 3 {
		return nil, fmt.Errorf("ожидалось три байта")
	}
	return out, nil
}

// validate проверяет параметры так же, как их проверит ядро при запуске,
// чтобы ошибка была видна сразу при добавлении конфигурации.
func (a *Amnezia) validate() error {
	for _, v := range []struct {
		name string
		val  int
	}{{"Jc", a.Jc}, {"Jmin", a.Jmin}, {"Jmax", a.Jmax}, {"S1", a.S1}, {"S2", a.S2}, {"S3", a.S3}, {"S4", a.S4}} {
		if v.val < 0 {
			return fmt.Errorf("%s не может быть отрицательным", v.name)
		}
	}
	if a.Jmax < a.Jmin {
		return fmt.Errorf("Jmax (%d) меньше Jmin (%d)", a.Jmax, a.Jmin)
	}
	if a.Jc > 0 && a.Jmax == 0 {
		return fmt.Errorf("при Jc нужен Jmax")
	}
	var ranges [4][2]uint64
	for i, h := range []string{a.H1, a.H2, a.H3, a.H4} {
		ranges[i] = [2]uint64{uint64(i + 1), uint64(i + 1)}
		if h == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(h, "-")
		var err error
		if ranges[i][0], err = strconv.ParseUint(strings.TrimSpace(lo), 10, 32); err != nil {
			return fmt.Errorf("H%d: ожидалось число или диапазон «a-b», получено %q", i+1, h)
		}
		ranges[i][1] = ranges[i][0]
		if isRange {
			if ranges[i][1], err = strconv.ParseUint(strings.TrimSpace(hi), 10, 32); err != nil || ranges[i][1] < ranges[i][0] {
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
	for i, v := range []string{a.I1, a.I2, a.I3, a.I4, a.I5} {
		if v != "" && (!strings.HasPrefix(v, "<") || !strings.HasSuffix(v, ">")) {
			return fmt.Errorf("I%d: ожидались теги вида <b 0x…><r 16>", i+1)
		}
	}
	if a.HeaderProtectionKey != "" {
		if err := checkKey(a.HeaderProtectionKey); err != nil {
			return fmt.Errorf("HeaderProtectionKey: %w", err)
		}
		for i, v := range []int{a.S1, a.S2, a.S3, a.S4} {
			if v < 12 {
				return fmt.Errorf("с HeaderProtectionKey S%d должен быть не меньше 12, а не %d", i+1, v)
			}
		}
	}
	return nil
}

// normRange проверяет «a» или «a-b» и возвращает «a-b»; «0», «off» и пусто
// означают «не задано».
func normRange(s string) (string, error) {
	switch strings.ToLower(s) {
	case "", "0", "off", "0-0":
		return "", nil
	}
	lo, hi, isRange := strings.Cut(s, "-")
	a, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 16)
	if err != nil {
		return "", fmt.Errorf("ожидалось число или диапазон «a-b», получено %q", s)
	}
	b := a
	if isRange {
		if b, err = strconv.ParseUint(strings.TrimSpace(hi), 10, 16); err != nil || b < a {
			return "", fmt.Errorf("некорректный диапазон %q", s)
		}
	}
	return fmt.Sprintf("%d-%d", a, b), nil
}

func parseOnOff(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no", "":
		return false, nil
	}
	return false, fmt.Errorf("ожидалось on или off, получено %q", s)
}
