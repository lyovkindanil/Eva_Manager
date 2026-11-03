package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
)

func TestUsers(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s := New(d)
		node, err := s.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		expires := time.Now().Add(30 * 24 * time.Hour)
		u, err := s.CreateUser(ctx, node.ID, "danil", "заметка", 1<<30, expires)
		if err != nil {
			t.Fatal(err)
		}
		if len(u.UUID) != 36 || u.UUID[8] != '-' {
			t.Errorf("uuid: %q", u.UUID)
		}
		if len(u.SubToken) != 32 {
			t.Errorf("токен подписки: %q", u.SubToken)
		}
		if u.Status != UserActive || u.TrafficLimit != 1<<30 || u.ExpiresAt.IsZero() {
			t.Errorf("пользователь: %+v", u)
		}
		// Имя уникально.
		if _, err := s.CreateUser(ctx, node.ID, "danil", "", 0, time.Time{}); err == nil {
			t.Error("дубль имени создан")
		}
		// Бессрочный пользователь.
		boris, err := s.CreateUser(ctx, node.ID, "boris", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if !boris.ExpiresAt.IsZero() {
			t.Errorf("срок: %v", boris.ExpiresAt)
		}

		// Инбаунд: доступ открывается всем существующим пользователям.
		in, err := inbound.NewVLESSReality(NewID(), node.ID, "NL-1", 443, "vpn.example.com", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CreateInbound(ctx, in); err != nil {
			t.Fatal(err)
		}
		ids, err := s.UserInboundIDs(ctx, u.ID)
		if err != nil || len(ids) != 1 || ids[0] != in.ID {
			t.Fatalf("доступы: %v, %v", ids, err)
		}
		// Новый пользователь получает доступ к существующему инбаунду.
		iva, err := s.CreateUser(ctx, node.ID, "iva", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		ids, _ = s.UserInboundIDs(ctx, iva.ID)
		if len(ids) != 1 || ids[0] != in.ID {
			t.Errorf("доступы нового пользователя: %v", ids)
		}

		// Активные для инбаунда: все, кроме выключенных.
		active, err := s.ActiveUsersForInbound(ctx, in.ID)
		if err != nil || len(active) != 3 {
			t.Fatalf("активные: %d, %v", len(active), err)
		}
		if err := s.SetUserDisabled(ctx, boris.ID, true); err != nil {
			t.Fatal(err)
		}
		active, _ = s.ActiveUsersForInbound(ctx, in.ID)
		if len(active) != 2 {
			t.Errorf("после выключения: %d", len(active))
		}

		// Трафик: общий счётчик — сумма, суточный — раздельно.
		if err := s.AddUserTraffic(ctx, u.ID, "2026-11-02", 100, 50); err != nil {
			t.Fatal(err)
		}
		if err := s.AddUserTraffic(ctx, u.ID, "2026-11-02", 10, 10); err != nil {
			t.Fatal(err)
		}
		got, err := s.UserByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TrafficUsed != 170 {
			t.Errorf("израсходовано: %d", got.TrafficUsed)
		}
		daily, err := s.UserTrafficDaily(ctx, u.ID, 7)
		if err != nil || len(daily) != 1 {
			t.Fatalf("суточные: %+v, %v", daily, err)
		}
		if daily[0].Uplink != 110 || daily[0].Downlink != 60 {
			t.Errorf("суточный трафик: %+v", daily[0])
		}
		if err := s.ResetUserTraffic(ctx, u.ID); err != nil {
			t.Fatal(err)
		}
		got, _ = s.UserByID(ctx, u.ID)
		if got.TrafficUsed != 0 {
			t.Errorf("после сброса: %d", got.TrafficUsed)
		}

		// Лимиты и срок меняются.
		if err := s.SetUserLimits(ctx, u.ID, 5<<30, time.Time{}); err != nil {
			t.Fatal(err)
		}
		got, _ = s.UserByID(ctx, u.ID)
		if got.TrafficLimit != 5<<30 || !got.ExpiresAt.IsZero() {
			t.Errorf("лимиты: %+v", got)
		}

		// Удаление: доступы и статистика уходят каскадом.
		if err := s.DeleteUser(ctx, u.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UserByID(ctx, u.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("после удаления: %v", err)
		}
		ids, _ = s.UserInboundIDs(ctx, u.ID)
		if len(ids) != 0 {
			t.Errorf("доступы после удаления: %v", ids)
		}
		daily, _ = s.UserTrafficDaily(ctx, u.ID, 7)
		if len(daily) != 0 {
			t.Errorf("суточные после удаления: %+v", daily)
		}
	})
}

func TestComputeStatus(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		u    User
		want UserStatus
	}{
		{"активный", User{Status: UserActive}, UserActive},
		{"выключен вручную", User{Status: UserDisabled, TrafficUsed: 10, TrafficLimit: 5}, UserDisabled},
		{"истёк срок", User{Status: UserActive, ExpiresAt: now.Add(-time.Minute)}, UserExpired},
		{"срок впереди", User{Status: UserActive, ExpiresAt: now.Add(time.Minute)}, UserActive},
		{"лимит исчерпан", User{Status: UserActive, TrafficUsed: 100, TrafficLimit: 100}, UserLimited},
		{"лимит не вышел", User{Status: UserActive, TrafficUsed: 99, TrafficLimit: 100}, UserActive},
		{"без лимита", User{Status: UserActive, TrafficUsed: 1 << 40}, UserActive},
		{"продление после лимита", User{Status: UserLimited, TrafficUsed: 0, TrafficLimit: 100}, UserActive},
		{"продление после срока", User{Status: UserExpired, ExpiresAt: now.Add(time.Hour)}, UserActive},
		{"срок и лимит вместе", User{Status: UserActive, ExpiresAt: now.Add(-time.Minute), TrafficUsed: 100, TrafficLimit: 100}, UserExpired},
	}
	for _, tc := range cases {
		if got := tc.u.ComputeStatus(now); got != tc.want {
			t.Errorf("%s: %s, ждали %s", tc.name, got, tc.want)
		}
	}
}

func TestInboundStore(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s := New(d)
		node, err := s.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		in, err := inbound.NewVLESSReality(NewID(), node.ID, "NL-1", 443, "vpn.example.com", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := in.EnableEncryption("native", "0rtt", 600, "100-111-1111.75-0-111.50-0-3333"); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateInbound(ctx, in); err != nil {
			t.Fatal(err)
		}
		// Типизированные настройки переживают запись и чтение целиком.
		got, err := s.InboundByName(ctx, node.ID, "NL-1")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Reality, in.Reality) {
			t.Errorf("настройки разошлись:\n%+v\n%+v", got.Reality, in.Reality)
		}
		// Битый инбаунд не сохраняется.
		bad := in
		bad.ID, bad.Name, bad.Port = NewID(), "bad", 0
		if err := s.CreateInbound(ctx, bad); err == nil {
			t.Error("битый инбаунд сохранён")
		}
		// Выключенный идёт после включённых.
		off, err := inbound.NewVLESSReality(NewID(), node.ID, "DE-1", 8443, "vpn2.example.com", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CreateInbound(ctx, off); err != nil {
			t.Fatal(err)
		}
		if err := s.SetInboundEnabled(ctx, off.ID, false); err != nil {
			t.Fatal(err)
		}
		list, err := s.Inbounds(ctx, node.ID)
		if err != nil || len(list) != 2 {
			t.Fatalf("список: %d, %v", len(list), err)
		}
		if !list[0].Enabled || list[1].Enabled {
			t.Errorf("порядок: %+v", list)
		}
		if err := s.DeleteInbound(ctx, off.ID); err != nil {
			t.Fatal(err)
		}
		list, _ = s.Inbounds(ctx, node.ID)
		if len(list) != 1 {
			t.Errorf("после удаления: %d", len(list))
		}
		if _, err := s.InboundByID(ctx, off.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("удалённый найден: %v", err)
		}
	})
}

func TestNewUUIDFormat(t *testing.T) {
	for range 100 {
		id := NewUUID()
		if len(id) != 36 || strings.Count(id, "-") != 4 || id[14] != '4' {
			t.Fatalf("uuid: %q", id)
		}
	}
}
