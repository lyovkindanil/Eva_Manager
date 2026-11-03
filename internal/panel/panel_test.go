package panel

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// fakeAPI ведёт себя как gRPC API Xray: те же ошибки «уже есть» и
// «не найден», счётчики трафика со сбросом.
type fakeAPI struct {
	mu      sync.Mutex
	users   map[string]map[string]string // тег → email → uuid
	traffic map[string]xray.Traffic
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{users: map[string]map[string]string{}, traffic: map[string]xray.Traffic{}}
}

func (f *fakeAPI) AddVLESSUser(_ context.Context, tag, email, uuid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.users[tag] == nil {
		f.users[tag] = map[string]string{}
	}
	if _, ok := f.users[tag][email]; ok {
		return fmt.Errorf("User %s already exists.", email)
	}
	f.users[tag][email] = uuid
	return nil
}

func (f *fakeAPI) RemoveUser(_ context.Context, tag, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[tag][email]; !ok {
		return fmt.Errorf("User %s not found.", email)
	}
	delete(f.users[tag], email)
	return nil
}

func (f *fakeAPI) InboundUsers(_ context.Context, tag string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for email := range f.users[tag] {
		out = append(out, email)
	}
	slices.Sort(out)
	return out, nil
}

func (f *fakeAPI) UserTraffic(_ context.Context, reset bool) (map[string]xray.Traffic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]xray.Traffic{}
	for id, t := range f.traffic {
		out[id] = t
	}
	if reset {
		f.traffic = map[string]xray.Traffic{}
	}
	return out, nil
}

func (f *fakeAPI) inCore(tag string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for email := range f.users[tag] {
		out = append(out, email)
	}
	slices.Sort(out)
	return out
}

func setup(t *testing.T, d *db.DB) (*store.Store, *store.Node, inbound.Inbound, *fakeAPI, *Panel) {
	t.Helper()
	ctx := context.Background()
	s := store.New(d)
	node, err := s.EnsureLocalNode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in, err := inbound.NewVLESSReality(store.NewID(), node.ID, "NL-1", 443, "vpn.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInbound(ctx, in); err != nil {
		t.Fatal(err)
	}
	api := newFakeAPI()
	p := &Panel{Store: s, API: api, NodeID: node.ID}
	return s, node, in, api, p
}

// Активные попадают в ядро, выключенные убираются; повторная сверка —
// идемпотентна.
func TestSyncUsers(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s, node, in, api, p := setup(t, d)
		danil, err := s.CreateUser(ctx, node.ID, "danil", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		boris, err := s.CreateUser(ctx, node.ID, "boris", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetUserDisabled(ctx, boris.ID, true); err != nil {
			t.Fatal(err)
		}
		if err := p.SyncUsers(ctx); err != nil {
			t.Fatal(err)
		}
		tag := in.Tag()
		if got := api.inCore(tag); !slices.Equal(got, []string{danil.ID}) {
			t.Fatalf("в ядре: %v", got)
		}
		// Повтор ничего не меняет.
		if err := p.SyncUsers(ctx); err != nil {
			t.Fatal(err)
		}
		if got := api.inCore(tag); !slices.Equal(got, []string{danil.ID}) {
			t.Fatalf("после повтора: %v", got)
		}
		// Включили бориса, выключили данила.
		if err := s.SetUserDisabled(ctx, boris.ID, false); err != nil {
			t.Fatal(err)
		}
		if err := s.SetUserDisabled(ctx, danil.ID, true); err != nil {
			t.Fatal(err)
		}
		if err := p.SyncUsers(ctx); err != nil {
			t.Fatal(err)
		}
		if got := api.inCore(tag); !slices.Equal(got, []string{boris.ID}) {
			t.Fatalf("после переключения: %v", got)
		}
		// Удалённый пользователь уходит из ядра.
		if err := s.DeleteUser(ctx, boris.ID); err != nil {
			t.Fatal(err)
		}
		if err := p.SyncUsers(ctx); err != nil {
			t.Fatal(err)
		}
		if got := api.inCore(tag); len(got) != 0 {
			t.Fatalf("после удаления: %v", got)
		}
	})
}

// Трафик пишется в базу, лимит и срок отключают, продление возвращает.
func TestCollectLimitsAndExpiry(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s, node, in, api, p := setup(t, d)
		now := time.Now()
		p.Now = func() time.Time { return now }
		u, err := s.CreateUser(ctx, node.ID, "danil", "", 100, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		tag := in.Tag()
		if err := p.SyncUsers(ctx); err != nil {
			t.Fatal(err)
		}

		// 60 байт — лимит не вышел, трафик записан.
		api.traffic[u.ID] = xray.Traffic{Uplink: 40, Downlink: 20}
		if err := p.CollectOnce(ctx); err != nil {
			t.Fatal(err)
		}
		got, _ := s.UserByID(ctx, u.ID)
		if got.TrafficUsed != 60 || got.Status != store.UserActive {
			t.Fatalf("после 60 байт: %+v", got)
		}
		day, _ := s.UserTrafficDaily(ctx, u.ID, 1)
		if len(day) != 1 || day[0].Uplink != 40 || day[0].Downlink != 20 || day[0].Day != now.UTC().Format("2006-01-02") {
			t.Errorf("суточные: %+v", day)
		}
		// Чужой email в статистике ядра игнорируется.
		api.traffic["чужой"] = xray.Traffic{Uplink: 1000}

		// Ещё 50 — лимит исчерпан: статус и удаление из ядра в том же тике.
		api.traffic[u.ID] = xray.Traffic{Uplink: 50}
		if err := p.CollectOnce(ctx); err != nil {
			t.Fatal(err)
		}
		got, _ = s.UserByID(ctx, u.ID)
		if got.TrafficUsed != 110 || got.Status != store.UserLimited {
			t.Fatalf("после лимита: %+v", got)
		}
		if got := api.inCore(tag); len(got) != 0 {
			t.Errorf("в ядре после лимита: %v", got)
		}

		// Сброс трафика — пользователь сам вернулся в строй на следующем тике.
		if err := s.ResetUserTraffic(ctx, u.ID); err != nil {
			t.Fatal(err)
		}
		if err := p.CollectOnce(ctx); err != nil {
			t.Fatal(err)
		}
		got, _ = s.UserByID(ctx, u.ID)
		if got.Status != store.UserActive {
			t.Errorf("после сброса: %s", got.Status)
		}
		if got := api.inCore(tag); !slices.Equal(got, []string{u.ID}) {
			t.Errorf("в ядре после сброса: %v", got)
		}

		// Срок истёк — отключение.
		if err := s.SetUserLimits(ctx, u.ID, 0, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		p.Now = func() time.Time { return now.Add(2 * time.Minute) }
		if err := p.CollectOnce(ctx); err != nil {
			t.Fatal(err)
		}
		got, _ = s.UserByID(ctx, u.ID)
		if got.Status != store.UserExpired {
			t.Errorf("после срока: %s", got.Status)
		}
		if got := api.inCore(tag); len(got) != 0 {
			t.Errorf("в ядре после срока: %v", got)
		}
	})
}
