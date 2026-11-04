package panel

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/awg/awgtest"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

// fakeAWG — процессы AmneziaWG в памяти: на каждый включённый интерфейс
// сервер awgtest.Server; после «запуска» панель присылает настройки, как
// с настоящим awg.Manager.
type fakeAWG struct {
	t     *testing.T
	mu    sync.Mutex
	socks map[string]string
	p     *Panel
}

func (f *fakeAWG) Reconcile(ctx context.Context, ifaces []awg.Interface) {
	for _, in := range ifaces {
		f.mu.Lock()
		_, ok := f.socks[in.ID]
		if !ok && in.Enabled {
			f.socks[in.ID] = awgtest.Server(f.t, in)
		}
		f.mu.Unlock()
		if !ok && in.Enabled {
			if err := f.p.PushAWG(ctx, in.ID); err != nil {
				f.t.Error(err)
			}
		}
	}
}

func (f *fakeAWG) Running(id string) (string, int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.socks[id]
	return s, 1, ok
}

func (f *fakeAWG) peers(t *testing.T, id string) int {
	s, _, _ := f.Running(id)
	st, err := (awg.Client{Socket: s}).Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(st)
}

// Панель без Xray, только AmneziaWG: пиры активных пользователей на
// сервере, трафик через туннель учитывается, по лимиту пир убирается.
func TestAWGUsers(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		st := store.New(d)
		node, err := st.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		danil, err := st.CreateUser(ctx, node.ID, "danil", "", 50_000, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateUser(ctx, node.ID, "boris", "", 0, time.Time{}); err != nil {
			t.Fatal(err)
		}
		in, err := awg.New(store.NewID(), node.ID, "AWG", "awg0", awgtest.FreeUDPPort(t), "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CreateAWG(ctx, in); err != nil {
			t.Fatal(err)
		}
		f := &fakeAWG{t: t, socks: map[string]string{}}
		pn := &Panel{Store: st, NodeID: node.ID, AWG: f}
		f.p = pn
		if err := pn.SyncUsers(ctx); err != nil {
			t.Fatal(err)
		}
		if n := f.peers(t, in.ID); n != 2 {
			t.Fatalf("пиров %d, ожидалось 2", n)
		}

		ua, err := st.UserAWGs(ctx, danil.ID)
		if err != nil || len(ua) != 1 {
			t.Fatal(ua, err)
		}
		conf, err := ua[0].Interface.ClientConf(ua[0].Peer, "")
		if err != nil {
			t.Fatal(err)
		}
		hc := awgtest.Client(t, conf)
		resp, err := hc.Get("http://" + in.ServerAddr().Addr().String() + "/bytes?n=100000")
		if err != nil {
			t.Fatal("через туннель:", err)
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if n != 100000 {
			t.Fatal(n)
		}

		if err := pn.CollectOnce(ctx); err != nil {
			t.Fatal(err)
		}
		u, _ := st.UserByID(ctx, danil.ID)
		if u.TrafficUsed < 100000 || u.Status != store.UserLimited {
			t.Fatalf("трафик %d, статус %s", u.TrafficUsed, u.Status)
		}
		if n := f.peers(t, in.ID); n != 1 {
			t.Fatalf("пиров %d после лимита, ожидался 1", n)
		}
		// Второй тик ничего не добавляет: счётчики берутся приращениями.
		used := u.TrafficUsed
		if err := pn.CollectOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if u, _ := st.UserByID(ctx, danil.ID); u.TrafficUsed != used {
			t.Fatalf("трафик вырос без передачи: %d → %d", used, u.TrafficUsed)
		}

		// Сброс трафика возвращает пир.
		if err := st.ResetUserTraffic(ctx, danil.ID); err != nil {
			t.Fatal(err)
		}
		if err := pn.CollectOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if n := f.peers(t, in.ID); n != 2 {
			t.Fatalf("пиров %d после сброса, ожидалось 2", n)
		}
	})
}
