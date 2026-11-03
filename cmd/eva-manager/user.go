package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/config"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

// userCmd — управление пользователями: add | list | show | del |
// enable | disable | reset-traffic. Панель подхватывает изменения из
// базы за один тик сборщика (30 секунд), перезапуск не нужен.
func userCmd(ctx context.Context, cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("нужна подкоманда: add | list | show | del | enable | disable | reset-traffic\n" + usage)
	}
	st, node, closeDB, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDB()
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("user add", flag.ContinueOnError)
		name := fs.String("name", "", "имя пользователя")
		limitGB := fs.Float64("limit-gb", 0, "лимит трафика в ГБ (0 — без лимита)")
		days := fs.Int("days", 0, "срок действия в днях (0 — бессрочно)")
		note := fs.String("note", "", "заметка")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("нужен --name")
		}
		var expires time.Time
		if *days > 0 {
			expires = time.Now().Add(time.Duration(*days) * 24 * time.Hour)
		}
		u, err := st.CreateUser(ctx, node.ID, *name, *note, int64(*limitGB*(1<<30)), expires)
		if err != nil {
			return err
		}
		fmt.Printf("Пользователь %q создан\n", u.Name)
		return printUserAccess(ctx, st, cfg, u)
	case "list":
		users, err := st.Users(ctx)
		if err != nil {
			return err
		}
		if len(users) == 0 {
			fmt.Println("Пользователей нет: eva-manager user add --name danil")
			return nil
		}
		for _, u := range users {
			limit := "∞"
			if u.TrafficLimit > 0 {
				limit = humanBytes(u.TrafficLimit)
			}
			expires := "бессрочно"
			if !u.ExpiresAt.IsZero() {
				expires = u.ExpiresAt.Format("2006-01-02")
			}
			fmt.Printf("%-16s %-9s %10s / %-10s до %s\n", u.Name, u.Status, humanBytes(u.TrafficUsed), limit, expires)
		}
		return nil
	case "show":
		u, err := userByNameFlag(ctx, st, args[1:])
		if err != nil {
			return err
		}
		fmt.Printf("%s (%s)\n  uuid: %s\n  трафик: %s / %s\n  срок: %s\n  заметка: %s\n",
			u.Name, u.Status, u.UUID, humanBytes(u.TrafficUsed), humanBytes(u.TrafficLimit),
			u.ExpiresAt.Format("2006-01-02 15:04"), u.Note)
		return printUserAccess(ctx, st, cfg, u)
	case "del", "enable", "disable", "reset-traffic":
		u, err := userByNameFlag(ctx, st, args[1:])
		if err != nil {
			return err
		}
		switch args[0] {
		case "del":
			err = st.DeleteUser(ctx, u.ID)
		case "enable":
			err = st.SetUserDisabled(ctx, u.ID, false)
		case "disable":
			err = st.SetUserDisabled(ctx, u.ID, true)
		case "reset-traffic":
			err = st.ResetUserTraffic(ctx, u.ID)
		}
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s — готово, панель применит за 30 секунд\n", u.Name, args[0])
		return nil
	}
	return errors.New("неизвестная подкоманда user " + args[0])
}

func userByNameFlag(ctx context.Context, st *store.Store, args []string) (*store.User, error) {
	fs := flag.NewFlagSet("user", flag.ContinueOnError)
	name := fs.String("name", "", "имя пользователя")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *name == "" {
		return nil, errors.New("нужен --name")
	}
	return st.UserByName(ctx, *name)
}

// printUserAccess печатает ссылки по всем включённым инбаундам с доступом
// и адрес подписки.
func printUserAccess(ctx context.Context, st *store.Store, cfg *config.Config, u *store.User) error {
	inbounds, err := st.UserInbounds(ctx, u.ID)
	if err != nil {
		return err
	}
	for _, in := range inbounds {
		link, err := in.Link(u.UUID, u.Name+" · "+in.Name)
		if err != nil {
			return err
		}
		fmt.Printf("  %s\n", link)
	}
	if len(inbounds) == 0 {
		fmt.Println("  (включённых инбаундов нет — добавьте: eva-manager inbound add ...)")
	}
	fmt.Printf("Подписка: %s\n", cfg.SubscriptionURL(u.SubToken))
	if cfg.SubURL == "" {
		fmt.Printf("  (адрес для этой машины; внешний задаётся в %s, слушать наружу — %s)\n", config.EnvSubURL, config.EnvSubListen)
	}
	return nil
}
