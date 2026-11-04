package panel

import (
	"context"
	"errors"
	"fmt"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// AWGRuntime — процессы AmneziaWG узла (awg.Manager; интерфейс для тестов).
type AWGRuntime interface {
	Reconcile(ctx context.Context, ifaces []awg.Interface)
	Running(id string) (socket string, gen int, ok bool)
}

// awgCounters — счётчики пиров интерфейса на прошлом тике. Счётчики UAPI
// растут с запуска устройства; gen меняется с перезапуском процесса — тогда
// отсчёт заново с нуля.
type awgCounters struct {
	gen  int
	seen map[string][2]int64 // public_key → rx, tx
}

// PushAWG — полные настройки интерфейса: ключ, порт, маскировка и пиры
// активных пользователей. Вызывается после каждого запуска процесса.
func (p *Panel) PushAWG(ctx context.Context, id string) error {
	in, err := p.awgByID(ctx, id)
	if err != nil {
		return err
	}
	sock, _, ok := p.AWG.Running(id)
	if !ok {
		return nil
	}
	peers, err := p.Store.ActiveAWGPeers(ctx, id)
	if err != nil {
		return err
	}
	cfg, err := in.DeviceUAPI(peers)
	if err != nil {
		return err
	}
	if err := (awg.Client{Socket: sock}).Set(ctx, cfg); err != nil {
		return err
	}
	p.log().Info("настройки AmneziaWG применены", "iface", in.Iface, "peers", len(peers))
	return nil
}

func (p *Panel) awgByID(ctx context.Context, id string) (*awg.Interface, error) {
	ifs, err := p.Store.AWGInterfaces(ctx, p.NodeID)
	if err != nil {
		return nil, err
	}
	for _, in := range ifs {
		if in.ID == id {
			return &in, nil
		}
	}
	return nil, fmt.Errorf("интерфейс AmneziaWG %s не найден", id)
}

// syncAWG — процессы по списку интерфейсов и пиры по активным
// пользователям: недостающие добавляются, лишние убираются, у остальных
// сессии не рвутся.
func (p *Panel) syncAWG(ctx context.Context) error {
	ifs, err := p.Store.AWGInterfaces(ctx, p.NodeID)
	if err != nil {
		return err
	}
	p.AWG.Reconcile(ctx, ifs)
	var errs []error
	for _, in := range ifs {
		if !in.Enabled {
			continue
		}
		if err := p.syncAWGPeers(ctx, in); err != nil {
			errs = append(errs, fmt.Errorf("AmneziaWG %s: %w", in.Iface, err))
		}
	}
	return errors.Join(errs...)
}

func (p *Panel) syncAWGPeers(ctx context.Context, in awg.Interface) error {
	sock, _, ok := p.AWG.Running(in.ID)
	if !ok {
		return nil // поднимется — получит всё в PushAWG
	}
	c := awg.Client{Socket: sock}
	have, err := c.Peers(ctx)
	if err != nil {
		return err
	}
	peers, err := p.Store.ActiveAWGPeers(ctx, in.ID)
	if err != nil {
		return err
	}
	want := map[string]awg.Peer{}
	for _, pr := range peers {
		k, err := pr.PublicKey()
		if err != nil {
			return err
		}
		want[k] = pr
	}
	var add, remove []awg.Peer
	for _, h := range have {
		if _, ok := want[h.PublicKey]; ok {
			delete(want, h.PublicKey)
			continue
		}
		remove = append(remove, awg.Peer{PublicKeyB64: h.PublicKey})
	}
	for _, pr := range want {
		add = append(add, pr)
	}
	if len(add)+len(remove) == 0 {
		return nil
	}
	rm, err := awg.RemoveUAPI(remove)
	if err != nil {
		return err
	}
	ad, err := awg.PeersUAPI(add)
	if err != nil {
		return err
	}
	if err := c.Set(ctx, rm+ad); err != nil {
		return err
	}
	p.log().Info("пиры AmneziaWG сверены", "iface", in.Iface, "added", len(add), "removed", len(remove))
	return nil
}

// collectAWG — приращения трафика пиров с прошлого тика, по пользователям.
func (p *Panel) collectAWG(ctx context.Context, into map[string]xray.Traffic) error {
	ifs, err := p.Store.AWGInterfaces(ctx, p.NodeID)
	if err != nil {
		return err
	}
	p.awgMu.Lock()
	defer p.awgMu.Unlock()
	if p.awgSeen == nil {
		p.awgSeen = map[string]*awgCounters{}
	}
	var errs []error
	for _, in := range ifs {
		sock, gen, ok := p.AWG.Running(in.ID)
		if !ok {
			continue
		}
		stats, err := (awg.Client{Socket: sock}).Peers(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("AmneziaWG %s: %w", in.Iface, err))
			continue
		}
		users, err := p.Store.AWGPeerUsers(ctx, in.ID)
		if err != nil {
			return err
		}
		prev := p.awgSeen[in.ID]
		if prev == nil || prev.gen != gen {
			prev = &awgCounters{gen: gen, seen: map[string][2]int64{}}
		}
		next := &awgCounters{gen: gen, seen: make(map[string][2]int64, len(stats))}
		for _, s := range stats {
			next.seen[s.PublicKey] = [2]int64{s.RxBytes, s.TxBytes}
			last := prev.seen[s.PublicKey]
			rx, tx := s.RxBytes-last[0], s.TxBytes-last[1]
			if rx < 0 || tx < 0 { // пир удалили и добавили — счётчик с нуля
				rx, tx = s.RxBytes, s.TxBytes
			}
			uid, ok := users[s.PublicKey]
			if !ok || rx+tx == 0 {
				continue
			}
			t := into[uid]
			t.Uplink += rx
			t.Downlink += tx
			into[uid] = t
		}
		p.awgSeen[in.ID] = next
	}
	return errors.Join(errs...)
}
