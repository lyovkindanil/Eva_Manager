// Package config читает настройки панели из переменных окружения.
//
// Все настройки необязательны: без них панель хранит данные в SQLite в
// каталоге данных и слушает только 127.0.0.1, чтобы до входа по паролю
// (этап 3) её не было видно снаружи.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
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
		ShutdownTimeout: 10 * time.Second,
	}
	if c.DatabaseURL == "" {
		c.DatabaseURL = "sqlite://" + filepath.Join(c.DataDir, "eva.db")
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return nil, fmt.Errorf("%s: ожидался адрес вида 127.0.0.1:8080: %w", EnvListen, err)
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

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
