package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/lyovkindanil/Eva_Manager/internal/config"
)

// adminCmd — eva-manager admin passwd --name admin: задать пароль (и
// создать администратора, если его нет). Восстановление доступа с сервера:
// все сессии администратора закрываются.
func adminCmd(ctx context.Context, cfg *config.Config, args []string) error {
	if len(args) == 0 || args[0] != "passwd" {
		return errors.New("нужна подкоманда: passwd\n" + usage)
	}
	fs := flag.NewFlagSet("admin passwd", flag.ContinueOnError)
	name := fs.String("name", "admin", "имя администратора")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	pass, err := readPassword()
	if err != nil {
		return err
	}
	st, _, closeDB, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDB()
	a, created, err := st.SetAdminPassword(ctx, *name, pass)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("Администратор %q создан\n", a.Username)
	} else {
		fmt.Printf("Пароль администратора %q изменён, его сессии закрыты\n", a.Username)
	}
	return nil
}

// readPassword — с терминала без эха и с повтором; из канала — первая
// строка (для скриптов: echo … | eva-manager admin passwd).
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("пароль не передан на стандартный ввод")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "Новый пароль: ")
	p1, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Ещё раз: ")
	p2, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(p1) != string(p2) {
		return "", errors.New("пароли не совпали")
	}
	return string(p1), nil
}
