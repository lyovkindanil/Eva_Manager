// Package inbound — типизированная модель инбаунда и два сборщика из неё:
// фрагмент конфига Xray (ConfigFragment) и ссылка для клиента (Link).
// Сырой JSON в базе не редактируется, поэтому конфиг и ссылка не могут
// разойтись — это главное отличие панели от 3x-ui (см. план, «Чем панель
// лучше 3x-ui», п. 1).
package inbound

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// b64 — кодировка ключей в ссылках и конфиге Xray.
var b64 = base64.RawURLEncoding

// ProtocolVLESS — протокол инбаундов этапа 1.
const ProtocolVLESS = "vless"

// Значения по умолчанию для нового инбаунда.
const (
	DefaultPort        = 443
	DefaultFlow        = "xtls-rprx-vision"
	DefaultFingerprint = "chrome"
)

// DefaultDests — проверенные сайты маскировки: отвечают по TLS 1.3 и h2,
// поэтому Xray может отдавать сканерам их содержимое. Кнопка «Проверить
// инбаунд» (план, п. 6) перепроверяет выбранный сайт из панели.
var DefaultDests = []string{
	"www.microsoft.com:443",
	"www.samsung.com:443",
	"gateway.icloud.com:443",
}

// Inbound — инбаунд панели: строка таблицы inbounds, где settings — JSON
// типизированной структуры Reality.
type Inbound struct {
	ID       string
	NodeID   string
	Name     string
	Protocol string
	Port     int
	// LinkHost — адрес сервера для ссылок (домен или IP); если пуст,
	// ссылку построить нельзя — его заполняет тот, кто её строит.
	LinkHost string
	Enabled  bool
	Reality  VLESSReality
}

// Tag — тег инбаунда в конфиге Xray и в вызовах gRPC API.
func (in Inbound) Tag() string { return "in-" + in.ID }

// VLESSReality — VLESS на tcp с маскировкой Reality и необязательным
// постквантовым VLESS Encryption (mlkem768x25519plus из Xray-core).
type VLESSReality struct {
	// Dest — сайт маскировки в форме «хост:порт» (как правило 443).
	Dest string `json:"dest"`
	// ServerNames — имена для Reality; обычно одно — хост из Dest.
	ServerNames []string `json:"server_names"`
	// PrivateKey — X25519 (32 байта в base64url). Публичный ключ для
	// ссылки выводится из него (PublicKey) и отдельно не хранится.
	PrivateKey string `json:"private_key"`
	// ShortIDs — hex-строки до 16 знаков; первый идёт в ссылки.
	ShortIDs []string `json:"short_ids"`
	// Fingerprint — uTLS-отпечаток в ссылке; новый Xray без него отвергает
	// соединение, поэтому по умолчанию DefaultFingerprint.
	Fingerprint string `json:"fingerprint"`
	// MLKEM — клиент обязан слать гибридный ключ X25519MLKEM768
	// (support-x25519mlkem768=true в ссылке): Xray 26.9.8+ иначе
	// не подтверждает Reality.
	MLKEM bool `json:"mlkem"`
	// Flow — режим потока; этап 1 использует DefaultFlow.
	Flow string `json:"flow"`
	// Encryption — постквантовое шифрование VLESS; nil — выключено
	// (в ссылке encryption=none, в конфиге "decryption":"none").
	Encryption *Encryption `json:"encryption,omitempty"`
}

// Encryption — VLESS Encryption: постквантовое шифрование протокола поверх
// Reality. Строка decryption для конфига Xray и параметр encryption для
// ссылки собираются из одних полей (decryptionString / encryptionString).
type Encryption struct {
	Mode string `json:"mode"` // native | xorpub | random
	RTT  string `json:"rtt"`  // 0rtt | 1rtt — идёт в ссылку
	// Seconds — срок билета 0-RTT в секундах (идёт в конфиг сервера);
	// обязателен для 0rtt, для 1rtt может быть 0.
	Seconds int `json:"seconds"`
	// Padding — паддинг пакетов рукопожатия в формате Xray, например
	// "100-111-1111.75-0-111.50-0-3333"; пусто — без паддинга.
	Padding string `json:"padding,omitempty"`
	// Seed — приватный ключ ML-KEM-768 (64 байта в base64url). Публичный
	// ключ (1184 байта) для ссылки выводится из него (ClientKey).
	Seed string `json:"seed"`
}

// NewVLESSReality — инбаунд VLESS Reality с разумными умолчаниями: ключи и
// short id генерируются, сайт маскировки — первый из проверенного списка,
// если dest не задан.
func NewVLESSReality(id, nodeID, name string, port int, linkHost, dest string) (Inbound, error) {
	priv, _, err := NewRealityKeypair()
	if err != nil {
		return Inbound{}, err
	}
	sid, err := NewShortID()
	if err != nil {
		return Inbound{}, err
	}
	if dest == "" {
		dest = DefaultDests[0]
	}
	host, _, err := net.SplitHostPort(dest)
	if err != nil {
		return Inbound{}, fmt.Errorf("сайт маскировки: %w", err)
	}
	in := Inbound{
		ID:       id,
		NodeID:   nodeID,
		Name:     name,
		Protocol: ProtocolVLESS,
		Port:     port,
		LinkHost: linkHost,
		Enabled:  true,
		Reality: VLESSReality{
			Dest:        dest,
			ServerNames: []string{host},
			PrivateKey:  priv,
			ShortIDs:    []string{sid},
			Fingerprint: DefaultFingerprint,
			MLKEM:       true,
			Flow:        DefaultFlow,
		},
	}
	return in, in.Validate()
}

// EnableEncryption включает VLESS Encryption: генерирует ключ ML-KEM-768
// (постквантовый; эфемерный обмен постквантово безопасен в любом случае).
func (in *Inbound) EnableEncryption(mode, rtt string, seconds int, padding string) error {
	seed, _, err := NewEncryptionSeed()
	if err != nil {
		return err
	}
	in.Reality.Encryption = &Encryption{Mode: mode, RTT: rtt, Seconds: seconds, Padding: padding, Seed: seed}
	return in.Validate()
}

var paddingRe = regexp.MustCompile(`^\d+-\d+-\d+(\.\d+-\d+-\d+)*$`)

// Validate проверяет инбаунд перед сборкой конфига или ссылки.
func (in Inbound) Validate() error {
	r := in.Reality
	if in.Protocol != ProtocolVLESS {
		return fmt.Errorf("протокол %q не поддерживается (этап 1: %s)", in.Protocol, ProtocolVLESS)
	}
	if in.Port < 1 || in.Port > 65535 {
		return fmt.Errorf("порт %d вне диапазона 1–65535", in.Port)
	}
	host, port, err := net.SplitHostPort(r.Dest)
	if err != nil || host == "" {
		return fmt.Errorf("сайт маскировки %q: нужен формат «хост:порт»", r.Dest)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("сайт маскировки %q: некорректный порт", r.Dest)
	}
	if len(r.ServerNames) == 0 {
		return fmt.Errorf("нужен хотя бы один serverName")
	}
	for _, s := range r.ServerNames {
		if s == "" || strings.ContainsAny(s, " \t/") {
			return fmt.Errorf("некорректный serverName %q", s)
		}
	}
	if _, err := r.PublicKey(); err != nil {
		return fmt.Errorf("приватный ключ Reality: %w", err)
	}
	if len(r.ShortIDs) == 0 {
		return fmt.Errorf("нужен хотя бы один shortId")
	}
	for _, s := range r.ShortIDs {
		if len(s) == 0 || len(s) > 16 || len(s)%2 != 0 {
			return fmt.Errorf("shortId %q: hex чётной длины до 16 знаков", s)
		}
		if _, err := hex.DecodeString(s); err != nil {
			return fmt.Errorf("shortId %q: не hex", s)
		}
	}
	if r.Fingerprint == "" {
		return fmt.Errorf("пустой отпечаток uTLS (fp)")
	}
	if r.Flow != DefaultFlow {
		return fmt.Errorf("flow %q не поддерживается (этап 1: %s)", r.Flow, DefaultFlow)
	}
	if e := r.Encryption; e != nil {
		switch e.Mode {
		case "native", "xorpub", "random":
		default:
			return fmt.Errorf("encryption: режим %q (native, xorpub или random)", e.Mode)
		}
		switch e.RTT {
		case "0rtt":
			if e.Seconds <= 0 {
				return fmt.Errorf("encryption: для 0rtt нужен срок билета (seconds > 0)")
			}
		case "1rtt":
		default:
			return fmt.Errorf("encryption: рукопожатие %q (0rtt или 1rtt)", e.RTT)
		}
		if e.Padding != "" && !paddingRe.MatchString(e.Padding) {
			return fmt.Errorf("encryption: паддинг %q не в формате Xray (например 100-111-1111.75-0-111.50-0-3333)", e.Padding)
		}
		if _, err := e.ClientKey(); err != nil {
			return fmt.Errorf("encryption: %w", err)
		}
	}
	return nil
}

// PublicKey — публичный ключ Reality, выведенный из приватного.
func (r VLESSReality) PublicKey() (string, error) {
	raw, err := b64.DecodeString(r.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("base64: %w", err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(priv.PublicKey().Bytes()), nil
}

// ClientKey — публичный ключ ML-KEM-768 для ссылки, выведенный из seed.
func (e Encryption) ClientKey() (string, error) {
	raw, err := b64.DecodeString(e.Seed)
	if err != nil {
		return "", fmt.Errorf("seed: base64: %w", err)
	}
	key, err := mlkem.NewDecapsulationKey768(raw)
	if err != nil {
		return "", fmt.Errorf("seed: %w", err)
	}
	return b64.EncodeToString(key.EncapsulationKey().Bytes()), nil
}

// decryptionString — поле decryption настроек VLESS в конфиге Xray:
// "none" или mlkem768x25519plus.<mode>.<seconds>s[.<padding>].<seed>.
func (in Inbound) decryptionString() string {
	e := in.Reality.Encryption
	if e == nil {
		return "none"
	}
	parts := []string{"mlkem768x25519plus", e.Mode, strconv.Itoa(e.Seconds) + "s"}
	if e.Padding != "" {
		parts = append(parts, e.Padding)
	}
	return strings.Join(append(parts, e.Seed), ".")
}

// encryptionString — параметр encryption в ссылке: "none" или
// mlkem768x25519plus.<mode>.<rtt>[.<padding>].<публичный ключ>.
func (in Inbound) encryptionString() (string, error) {
	e := in.Reality.Encryption
	if e == nil {
		return "none", nil
	}
	pub, err := e.ClientKey()
	if err != nil {
		return "", err
	}
	parts := []string{"mlkem768x25519plus", e.Mode, e.RTT}
	if e.Padding != "" {
		parts = append(parts, e.Padding)
	}
	return strings.Join(append(parts, pub), "."), nil
}

// AltPorts — порты для инбаунда, когда 443 занят (другой панелью или
// веб-сервером): привычные для HTTPS, их реже режут провайдеры.
var AltPorts = []int{443, 8443, 2053, 2083, 2087, 9443}

// NextPort — первый порт из AltPorts (потом 10443, 10444…), не занятый
// инбаундами узла и, если busy не nil, другими программами.
func NextPort(existing []Inbound, busy func(int) bool) int {
	used := map[int]bool{}
	for _, in := range existing {
		used[in.Port] = true
	}
	free := func(p int) bool { return !used[p] && (busy == nil || !busy(p)) }
	for _, p := range AltPorts {
		if free(p) {
			return p
		}
	}
	for p := 10443; p < 65535; p++ {
		if free(p) {
			return p
		}
	}
	return DefaultPort
}
