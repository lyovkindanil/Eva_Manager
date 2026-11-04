// Команда eva-manager — веб-панель для серверов на Xray-core и AmneziaWG.
//
//	eva-manager serve        запустить панель (по умолчанию)
//	eva-manager apply        применить конфиг из базы к работающему ядру
//	eva-manager awg-run      процесс интерфейса AmneziaWG (запускает панель)
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
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/server"
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
	case "awg-run":
		return awgRunCmd(ctx, os.Args[2:])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch cmd {
	case "token":
		tok, err := server.LoadToken(cfg.TokenFile())
		if err != nil {
			return err
		}
		fmt.Println(tok)
		return nil
	case "serve":
		return serve(ctx, cfg, log)
	case "inbound":
		return inboundCmd(ctx, cfg, os.Args[2:])
	case "user":
		return userCmd(ctx, cfg, os.Args[2:])
	case "awg":
		return awgCmd(ctx, cfg, os.Args[2:])
	case "admin":
		return adminCmd(ctx, cfg, os.Args[2:])
	case "apply":
		return applyCmd(ctx, cfg)
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
		st, node, closeDB, err := openStore(ctx, cfg)
		if err != nil {
			return err
		}
		defer closeDB()
		inbs, err := st.Inbounds(ctx, node.ID)
		if err != nil {
			return err
		}
		b, err := inbound.BuildConfig(cfg.XrayAPI, inbs)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(b, '\n'))
		return err
	}
	return errors.New("неизвестная команда " + cmd + "\n" + usage)
}

const usage = `Использование: eva-manager <команда>

  serve          запустить панель (по умолчанию)
  inbound        инбаунды: add | list | del
  awg            AmneziaWG: add | list | del | enable | disable
  user           пользователи: add | list | show | conf | del | enable | disable | reset-traffic
  apply          применить конфиг из базы к работающему ядру и AmneziaWG
  admin          администратор веб-интерфейса: passwd [--name admin]
  token          токен для API, CLI и мастера первого запуска
  migrate        применить миграции базы и выйти
  xray-config    напечатать конфиг Xray (из базы)
  version        версия панели

Переменные окружения:
  EVA_DATA_DIR      каталог данных (по умолчанию /var/lib/eva-manager)
  EVA_DATABASE_URL  sqlite:///путь/eva.db (по умолчанию в EVA_DATA_DIR) или postgres://…
  EVA_LISTEN        адрес панели (по умолчанию 127.0.0.1:8080)
  EVA_XRAY_BIN      путь к xray (по умолчанию ищется в PATH)
  EVA_XRAY_API      адрес gRPC API Xray, только loopback (по умолчанию 127.0.0.1:10085)
  EVA_SUB_LISTEN    отдельный адрес только для подписок, например 0.0.0.0:2096
  EVA_SUB_URL       внешний адрес подписок, например https://vpn.example.com:2096
  EVA_AWG_SOCKET_DIR каталог UAPI-сокетов AmneziaWG (по умолчанию /var/run/amneziawg, как у awg show)
  EVA_TOKEN         токен администратора для eva-manager apply (по умолчанию EVA_DATA_DIR/admin.token)
`
