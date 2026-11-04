package store

import (
	"context"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
)

func TestAWG(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s := New(d)
		node, err := s.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		danil, err := s.CreateUser(ctx, node.ID, "danil", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		in, err := awg.New(NewID(), node.ID, "NL AWG", "awg0", 0, "vpn.example.com")
		if err != nil {
			t.Fatal(err)
		}
		in.Settings.Subnet = "10.8.0.0/29" // адреса .2–.6
		if err := s.CreateAWG(ctx, in); err != nil {
			t.Fatal(err)
		}
		// Имя сетевого интерфейса на узле уникально.
		dup := in
		dup.ID, dup.Name, dup.Port = NewID(), "другое", 51821
		if err := s.CreateAWG(ctx, dup); err == nil {
			t.Fatal("дубль awg0 создан")
		}
		// И порт.
		dup.Iface, dup.Port = "awg1", in.Port
		if err := s.CreateAWG(ctx, dup); err == nil {
			t.Fatal("дубль порта создан")
		}

		// Существующий пользователь получил пир при создании интерфейса,
		// новый — при создании пользователя.
		boris, err := s.CreateUser(ctx, node.ID, "boris", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		peers, err := s.ActiveAWGPeers(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(peers) != 2 || peers[0].UserID != danil.ID || peers[0].Address.String() != "10.8.0.2" ||
			peers[1].UserID != boris.ID || peers[1].Address.String() != "10.8.0.3" || peers[0].PrivateKey == peers[1].PrivateKey {
			t.Fatalf("%+v", peers)
		}
		keys, err := s.AWGPeerUsers(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		pub, _ := peers[1].PublicKey()
		if keys[pub] != boris.ID {
			t.Fatalf("%v", keys)
		}

		// Выключенный пользователь — не на сервере, но пир (ключ, адрес)
		// сохраняется: включение вернёт ту же конфигурацию.
		if err := s.SetUserDisabled(ctx, boris.ID, true); err != nil {
			t.Fatal(err)
		}
		if peers, _ := s.ActiveAWGPeers(ctx, in.ID); len(peers) != 1 {
			t.Fatalf("%+v", peers)
		}
		ua, err := s.UserAWGs(ctx, boris.ID)
		if err != nil || len(ua) != 1 || ua[0].Peer.Address.String() != "10.8.0.3" || ua[0].Interface.Settings != in.Settings {
			t.Fatalf("%+v %v", ua, err)
		}

		// Освободившийся адрес выдаётся снова; подсеть кончается с ошибкой.
		if err := s.DeleteUser(ctx, danil.ID); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"a", "b", "c", "d"} {
			if _, err := s.CreateUser(ctx, node.ID, name, "", 0, time.Time{}); err != nil {
				t.Fatal(name, err)
			}
		}
		a, _ := s.UserByName(ctx, "a")
		if ua, _ := s.UserAWGs(ctx, a.ID); len(ua) != 1 || ua[0].Peer.Address.String() != "10.8.0.2" {
			t.Fatalf("%+v", ua)
		}
		if _, err := s.CreateUser(ctx, node.ID, "e", "", 0, time.Time{}); err == nil {
			t.Fatal("подсеть /29 не кончилась")
		}

		// Выключенный интерфейс не попадает к пользователю.
		if err := s.SetAWGEnabled(ctx, in.ID, false); err != nil {
			t.Fatal(err)
		}
		if ua, _ := s.UserAWGs(ctx, a.ID); len(ua) != 0 {
			t.Fatalf("%+v", ua)
		}
		got, err := s.AWGByName(ctx, node.ID, "awg0")
		if err != nil || got.Enabled || got.Name != "NL AWG" {
			t.Fatalf("%+v %v", got, err)
		}
		if err := s.DeleteAWG(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
		if l, _ := s.AWGInterfaces(ctx, ""); len(l) != 0 {
			t.Fatalf("%+v", l)
		}
	})
}
