package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/lyovkindanil/Eva_Manager/internal/config"
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	"github.com/lyovkindanil/Eva_Manager/internal/sysnet"
)

// inboundCmd — управление инбаундами: add | list | del. Изменения пишутся
// в базу и применяются к работающему ядру командой `eva-manager apply`
// (с проверкой конфига и откатом).
func inboundCmd(ctx context.Context, cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("нужна подкоманда: add | list | del\n" + usage)
	}
	st, node, closeDB, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDB()
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("inbound add", flag.ContinueOnError)
		name := fs.String("name", "", "имя инбаунда, например NL-1")
		host := fs.String("host", "", "адрес сервера для ссылок (домен или IP)")
		port := fs.Int("port", 0, "порт (по умолчанию 443, если занят — 8443, 2053…)")
		dest := fs.String("dest", "", "сайт маскировки «хост:443» (по умолчанию из проверенного списка)")
		enc := fs.Bool("encryption", false, "включить VLESS Encryption (mlkem768x25519plus, native/0rtt/600s)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" || *host == "" {
			return errors.New("нужны --name и --host")
		}
		existing, err := st.Inbounds(ctx, node.ID)
		if err != nil {
			return err
		}
		if *port == 0 {
			*port = inbound.NextPort(existing, sysnet.TCPBusy)
		} else {
			for _, e := range existing {
				if e.Port == *port {
					return fmt.Errorf("порт %d уже у инбаунда %q", *port, e.Name)
				}
			}
			if sysnet.TCPBusy(*port) {
				return fmt.Errorf("порт %d занят другой программой (другой панелью или веб-сервером): укажите другой --port или не указывайте его", *port)
			}
		}
		in, err := inbound.NewVLESSReality(store.NewID(), node.ID, *name, *port, *host, *dest)
		if err != nil {
			return err
		}
		if *enc {
			if err := in.EnableEncryption("native", "0rtt", 600, ""); err != nil {
				return err
			}
		}
		if err := st.CreateInbound(ctx, in); err != nil {
			return err
		}
		pbk, _ := in.Reality.PublicKey()
		fmt.Printf("Инбаунд %q создан: %s:%d, маскировка %s\n", in.Name, in.LinkHost, in.Port, in.Reality.Dest)
		fmt.Printf("  pbk %s\n  sid %s\n  encryption: %s\n", pbk, in.Reality.ShortIDs[0], encState(in))
		fmt.Println("Примените к работающему ядру: eva-manager apply")
		return nil
	case "list":
		list, err := st.Inbounds(ctx, node.ID)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("Инбаундов нет: eva-manager inbound add --name NL-1 --host vpn.example.com")
			return nil
		}
		for _, in := range list {
			state := "вкл"
			if !in.Enabled {
				state = "ВЫКЛ"
			}
			fmt.Printf("%-4s %-12s порт %-6d маскировка %-28s хост %-24s %s\n",
				state, in.Name, in.Port, in.Reality.Dest, in.LinkHost, encState(in))
		}
		return nil
	case "del":
		fs := flag.NewFlagSet("inbound del", flag.ContinueOnError)
		name := fs.String("name", "", "имя инбаунда")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		in, err := st.InboundByName(ctx, node.ID, *name)
		if err != nil {
			return err
		}
		if err := st.DeleteInbound(ctx, in.ID); err != nil {
			return err
		}
		fmt.Printf("Инбаунд %q удалён. Примените к работающему ядру: eva-manager apply\n", in.Name)
		return nil
	}
	return errors.New("неизвестная подкоманда inbound " + args[0])
}

func encState(in inbound.Inbound) string {
	if in.Reality.Encryption == nil {
		return "encryption выключен"
	}
	e := in.Reality.Encryption
	return "encryption " + strings.Join([]string{e.Mode, e.RTT}, "/")
}
