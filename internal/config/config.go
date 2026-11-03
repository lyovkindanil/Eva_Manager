// Package config читает настройки панели из переменных окружения.
//
// Все настройки необязательны: без них панель хранит данные в SQLite в
// каталоге данных и слушает только 127.0.0.1, чтобы до входа по паролю
// (этап 3) её не было видно снаружи.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config — настройки процесса панели.
type Config struct {
	// DataDir — каталог данных: база SQLite, конфиги Xray, журналы.
	DataDir string
	// DatabaseURL — sqlite:///путь/к/файлу.db или postgres://…
	DatabaseURL string
	// Listen — адрес веб-интерфейса и API панели.
	Listen string
	// XrayBin — путь к исполняемому файлу Xray; пусто — искать xray в PATH.
	XrayBin string
	// XrayAPI — адрес gRPC API Xray, только 127.0.0.1.
	XrayAPI string
	// SubListen — отдельный адрес только для подписок (/sub/…), например
	// 0.0.0.0:2096, когда панель слушает 127.0.0.1; пусто — подписки
	// отдаются только на Listen.
	SubListen string
	// SubURL — внешний адрес подписок, например https://vpn.example.com:2096;
	// из него CLI печатает ссылку на подписку. Пусто — адрес из SubListen
	// или Listen.
	SubURL string
	// ShutdownTimeout — сколько ждать завершения запросов при остановке.
	ShutdownTimeout time.Duration
}

// Переменные окружения.
const (
	EnvDataDir     = "EVA_DATA_DIR"
	EnvDatabaseURL = "EVA_DATABASE_URL"
	EnvListen      = "EVA_LISTEN"
	EnvXrayBin     = "EVA_XRAY_BIN"
	EnvXrayAPI     = "EVA_XRAY_API"
	EnvSubListen   = "EVA_SUB_LISTEN"
	EnvSubURL      = "EVA_SUB_URL"
	// EnvToken — токен администратора для команд CLI вместо файла.
	EnvToken = "EVA_TOKEN"
)

// DefaultDataDir — каталог данных при установке на сервер.
const DefaultDataDir = "/var/lib/eva-manager"

// Load читает настройки из окружения и проверяет их.
func Load() (*Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (*Config, error) {
	c := &Config{
		DataDir:         or(getenv(EnvDataDir), DefaultDataDir),
		DatabaseURL:     getenv(EnvDatabaseURL),
		Listen:          or(getenv(EnvListen), "127.0.0.1:8080"),
		XrayBin:         getenv(EnvXrayBin),
		XrayAPI:         or(getenv(EnvXrayAPI), "127.0.0.1:10085"),
		SubListen:       getenv(EnvSubListen),
		SubURL:          strings.TrimRight(getenv(EnvSubURL), "/"),
		ShutdownTimeout: 10 * time.Second,
	}
	if c.DatabaseURL == "" {
		c.DatabaseURL = "sqlite://" + filepath.Join(c.DataDir, "eva.db")
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return nil, fmt.Errorf("%s: ожидался адрес вида 127.0.0.1:8080: %w", EnvListen, err)
	}
	if c.SubListen != "" {
		if _, _, err := net.SplitHostPort(c.SubListen); err != nil {
			return nil, fmt.Errorf("%s: ожидался адрес вида 0.0.0.0:2096: %w", EnvSubListen, err)
		}
	}
	if c.SubURL != "" {
		u, err := url.Parse(c.SubURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" {
			return nil, fmt.Errorf("%s: ожидался адрес вида https://vpn.example.com:2096", EnvSubURL)
		}
	}
	host, _, err := net.SplitHostPort(c.XrayAPI)
	if err != nil {
		return nil, fmt.Errorf("%s: ожидался адрес вида 127.0.0.1:10085: %w", EnvXrayAPI, err)
	}
	// API Xray управляет пользователями без пароля: наружу его не выпускаем.
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("%s: API Xray может слушать только 127.0.0.1 или ::1, а не %q", EnvXrayAPI, host)
	}
	return c, nil
}

// TokenFile — файл с токеном администратора в каталоге данных.
func (c *Config) TokenFile() string { return filepath.Join(c.DataDir, "admin.token") }

// LocalURL — адрес для обращения к панели с этой же машины: 0.0.0.0 и
// :: заменяются на 127.0.0.1.
func LocalURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// SubscriptionURL — адрес подписки с токеном tok для клиентов.
func (c *Config) SubscriptionURL(tok string) string {
	base := c.SubURL
	switch {
	case base != "":
	case c.SubListen != "":
		base = LocalURL(c.SubListen)
	default:
		base = LocalURL(c.Listen)
	}
	return base + "/sub/" + tok
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
