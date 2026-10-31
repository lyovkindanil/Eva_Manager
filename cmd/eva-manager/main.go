// Команда eva-manager — веб-панель для серверов на Xray-core.
//
//	eva-manager serve        запустить панель (по умолчанию)
//	eva-manager migrate      применить миграции базы и выйти
//	eva-manager xray-config  напечатать базовый конфиг Xray
//	eva-manager version      версия панели
//
// Настройки — переменные окружения EVA_*, см. internal/config.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/lyovkindanil/Eva_Manager/internal/buildinfo"
	"github.com/lyovkindanil/Eva_Manager/internal/config"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cmd, log); err != nil {
		fmt.Fprintln(os.Stderr, "eva-manager:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, log *slog.Logger) error {
	switch cmd {
	case "version", "-v", "--version":
		fmt.Println(buildinfo.Version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch cmd {
	case "serve":
		return serve(ctx, cfg, log)
	case "migrate":
		d, err := db.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer d.Close()
		v, err := d.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("база %s, схема версии %d\n", d.Dialect, v)
		return nil
	case "xray-config":
		b, err := xray.BaseConfig(cfg.XrayAPI)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(b, '\n'))
		return err
	}
	return errors.New("неизвестная команда " + cmd + "\n" + usage)
}

const usage = `Использование: eva-manager [serve | migrate | xray-config | version]

Переменные окружения:
  EVA_DATA_DIR      каталог данных (по умолчанию /var/lib/eva-manager)
  EVA_DATABASE_URL  sqlite:///путь/eva.db (по умолчанию в EVA_DATA_DIR) или postgres://…
  EVA_LISTEN        адрес панели (по умолчанию 127.0.0.1:8080)
  EVA_XRAY_BIN      путь к xray (по умолчанию ищется в PATH)
  EVA_XRAY_API      адрес gRPC API Xray, только loopback (по умолчанию 127.0.0.1:10085)
`
