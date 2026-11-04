package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/config"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

// awgCmd — интерфейсы AmneziaWG: add | list | del | enable | disable.
// Изменения пишутся в базу; работающая панель подхватывает их за 30
// секунд или сразу по `eva-manager apply`.
func awgCmd(ctx context.Context, cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("нужна подкоманда: add | list | del | enable | disable\n" + usage)
	}
	st, node, closeDB, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDB()
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("awg add", flag.ContinueOnError)
		name := fs.String("name", "", "название для клиентов (по умолчанию AmneziaWG)")
		iface := fs.String("iface", "", "сетевой интерфейс на сервере (по умолчанию awg0, awg1…)")
		host := fs.String("host", "", "адрес сервера для клиентов (домен или IP)")
		port := fs.Int("port", 0, "UDP-порт (по умолчанию 51820, у следующих — 51821…)")
		subnet := fs.String("subnet", "", "подсеть клиентов (по умолчанию 10.8.0.0/24, у следующих — 10.9.0.0/24…)")
		dns := fs.String("dns", awg.DefaultDNS, "DNS для клиентов")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *host == "" {
			return errors.New("нужен --host")
		}
		existing, err := st.AWGInterfaces(ctx, node.ID)
		if err != nil {
			return err
		}
		in, err := awg.NewOnNode(store.NewID(), node.ID, awg.Options{
			Name: *name, Iface: *iface, Host: *host, Subnet: *subnet, DNS: *dns, Port: *port}, existing)
		if err != nil {
			return err
		}
		if err := st.CreateAWG(ctx, in); err != nil {
			return err
		}
		pub, _ := awg.PublicKey(in.Settings.PrivateKey)
		fmt.Printf("AmneziaWG %q создан: %s, UDP %s, подсеть %s\n  ключ сервера %s\n", in.Name, in.Iface, in.Endpoint(), in.Settings.Subnet, pub)
		fmt.Println("Пиры выданы всем пользователям. Панель поднимет интерфейс за 30 секунд или сразу: eva-manager apply")
		return nil
	case "list":
		list, err := st.AWGInterfaces(ctx, node.ID)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("AmneziaWG нет: eva-manager awg add --host vpn.example.com")
			return nil
		}
		for _, in := range list {
			state := "вкл"
			if !in.Enabled {
				state = "ВЫКЛ"
			}
			fmt.Printf("%-4s %-16s %-7s %-28s подсеть %s\n", state, in.Name, in.Iface, in.Endpoint(), in.Settings.Subnet)
		}
		return nil
	case "del", "enable", "disable":
		fs := flag.NewFlagSet("awg "+args[0], flag.ContinueOnError)
		name := fs.String("name", "", "название или сетевой интерфейс (awg0)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		in, err := st.AWGByName(ctx, node.ID, *name)
		if err != nil {
			return fmt.Errorf("AmneziaWG %q: %w", *name, err)
		}
		switch args[0] {
		case "del":
			err = st.DeleteAWG(ctx, in.ID)
		default:
			err = st.SetAWGEnabled(ctx, in.ID, args[0] == "enable")
		}
		if err != nil {
			return err
		}
		fmt.Printf("AmneziaWG %q: %s — готово, панель применит за 30 секунд или сразу: eva-manager apply\n", in.Name, args[0])
		return nil
	}
	return errors.New("неизвестная подкоманда awg " + args[0])
}

// awgRunCmd — процесс одного интерфейса AmneziaWG (его запускает панель).
func awgRunCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("awg-run", flag.ContinueOnError)
	iface := fs.String("iface", "", "сетевой интерфейс")
	addr := fs.String("addr", "", "адрес сервера с маской подсети, например 10.8.0.1/24")
	mtu := fs.Int("mtu", awg.DefaultMTU, "MTU")
	sockDir := fs.String("socket-dir", awg.SocketDir, "каталог UAPI-сокета")
	nat := fs.Bool("nat", true, "выпускать клиентов в интернет (ip_forward и iptables)")
	private := fs.Bool("allow-private", false, "пускать клиентов в частные сети за сервером")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := awg.ParseAddr(*addr)
	if err != nil {
		return err
	}
	// Вывод читает панель и пишет в свой журнал — время и уровень уже там.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey || a.Key == slog.LevelKey {
			return slog.Attr{}
		}
		return a
	}}))
	return awg.Run(ctx, awg.RunOptions{Iface: *iface, Addr: p, MTU: *mtu, SocketDir: *sockDir, NAT: *nat, AllowPrivate: *private, Log: log})
}
