// Package panel связывает базу и ядро Xray: держит в работающем ядре
// нужных пользователей, раз в 30 секунд забирает их трафик и применяет
// лимиты и сроки.
package panel

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// Tick — период сборщика трафика. Отключение по лимиту или сроку случается
// не позже чем через Tick после события — в рамках требования MVP
// «не позже чем через 1 минуту».
const Tick = 30 * time.Second

// XrayAPI — часть gRPC API Xray, нужная панели (интерфейс для тестов).
type XrayAPI interface {
	AddVLESSUser(ctx context.Context, tag, email, uuid string) error
	RemoveUser(ctx context.Context, tag, email string) error
	InboundUsers(ctx context.Context, tag string) ([]string, error)
	UserTraffic(ctx context.Context, reset bool) (map[string]xray.Traffic, error)
}

// Core — владелец работающего ядра: применяет конфиг с проверкой и
// откатом (интерфейс для тестов).
type Core interface {
	Apply(ctx context.Context, config []byte) error
}

// Panel — локальный узел: база панели плюс работающее ядро.
type Panel struct {
	Store *store.Store
	API   XrayAPI
	// Core — горячее применение конфига; nil — недоступно (ядром
	// управляет кто-то другой).
	Core Core
	// APIAddr — адрес gRPC API Xray, нужен для сборки конфига.
	APIAddr string
	NodeID  string
	Log     *slog.Logger
	// Now — часы (в тестах подменяются); nil — time.Now.
	Now func() time.Time
}

func (p *Panel) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Panel) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// ApplyConfig собирает конфиг Xray из инбаундов базы и применяет его к
// ядру без перезапуска панели. Попытка — успешная или нет — пишется в
// историю версий: последняя с TestOK — рабочая, по ней делается откат.
// При успехе сверяет пользователей с новым ядром (инбаунды могли
// появиться или исчезнуть).
func (p *Panel) ApplyConfig(ctx context.Context) error {
	if p.Core == nil {
		return errors.New("ядро не управляется панелью, применение недоступно")
	}
	inbounds, err := p.Store.Inbounds(ctx, p.NodeID)
	if err != nil {
		return err
	}
	config, err := inbound.BuildConfig(p.APIAddr, inbounds)
	if err != nil {
		return err
	}
	applyErr := p.Core.Apply(ctx, config)
	output := ""
	if applyErr != nil {
		output = applyErr.Error()
	}
	if _, err := p.Store.SaveConfigVersion(ctx, p.NodeID, string(config), applyErr == nil, output); err != nil {
		p.log().Warn("версия конфига не записана", "err", err)
	}
	if applyErr != nil {
		return applyErr
	}
	p.log().Info("конфиг применён к ядру", "inbounds", len(inbounds))
	return p.SyncUsers(ctx)
}

// SyncUsers сверяет пользователей каждого включённого инбаунда с базой:
// добавляет активных, убирает остальных (выключенных, истёкших, с
// исчерпанным лимитом, удалённых или потерявших доступ к инбаунду).
// Идемпотентно: ответы Xray «уже есть» и «не найден» — не ошибки.
func (p *Panel) SyncUsers(ctx context.Context) error {
	inbounds, err := p.Store.Inbounds(ctx, p.NodeID)
	if err != nil {
		return err
	}
	var errs []error
	for _, in := range inbounds {
		if !in.Enabled {
			continue
		}
		if err := p.syncInbound(ctx, in); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Panel) syncInbound(ctx context.Context, in inbound.Inbound) error {
	users, err := p.Store.ActiveUsersForInbound(ctx, in.ID)
	if err != nil {
		return err
	}
	want := make(map[string]string, len(users)) // email (id) → uuid
	for _, u := range users {
		want[u.ID] = u.UUID
	}
	tag := in.Tag()
	have, err := p.API.InboundUsers(ctx, tag)
	if err != nil {
		return err
	}
	for _, email := range have {
		if _, ok := want[email]; ok {
			continue
		}
		if err := p.API.RemoveUser(ctx, tag, email); err != nil && !xray.IsUserNotFound(err) {
			return err
		}
		p.log().Info("пользователь убран из ядра", "inbound", in.Name, "id", email)
	}
	for email, uuid := range want {
		if slices.Contains(have, email) {
			continue
		}
		if err := p.API.AddVLESSUser(ctx, tag, email, uuid); err != nil && !xray.IsUserExists(err) {
			return err
		}
		p.log().Info("пользователь добавлен в ядро", "inbound", in.Name, "id", email)
	}
	return nil
}

// CollectOnce — один тик сборщика: забрать приращения трафика из ядра
// (со сбросом счётчиков), записать в базу, пересчитать статусы и при
// изменениях сразу сверить пользователей с ядром.
func (p *Panel) CollectOnce(ctx context.Context) error {
	traffic, err := p.API.UserTraffic(ctx, true)
	if err != nil {
		return err
	}
	users, err := p.Store.Users(ctx)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(users))
	for _, u := range users {
		known[u.ID] = true
	}
	now := p.now()
	day := now.UTC().Format("2006-01-02")
	for id, t := range traffic {
		if !known[id] || (t.Uplink == 0 && t.Downlink == 0) {
			continue
		}
		if err := p.Store.AddUserTraffic(ctx, id, day, t.Uplink, t.Downlink); err != nil {
			return err
		}
	}
	// Статусы пересчитываются по фактам: продление (сброс трафика, новый
	// срок) само возвращает пользователя в строй.
	changed := false
	for _, u := range users {
		if t, ok := traffic[u.ID]; ok {
			u.TrafficUsed += t.Uplink + t.Downlink
		}
		if st := u.ComputeStatus(now); st != u.Status {
			if err := p.Store.SetUserStatus(ctx, u.ID, st); err != nil {
				return err
			}
			p.log().Info("статус пользователя", "name", u.Name, "status", st)
			changed = true
		}
	}
	if changed {
		return p.SyncUsers(ctx)
	}
	return nil
}

// Run — цикл сборщика до отмены ctx: SyncUsers при старте (с повторами,
// пока ядро не поднимется), дальше сверка и сбор каждые Tick. Попутно
// ловит перезапуск ядра: после него список пользователей пуст, и
// SyncUsers возвращает всех на место.
func (p *Panel) Run(ctx context.Context) {
	for {
		if err := p.SyncUsers(ctx); err == nil {
			break
		} else {
			p.log().Warn("сверка с ядром: повторю", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	tick := time.NewTicker(Tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if err := p.SyncUsers(ctx); err != nil {
			p.log().Warn("сверка с ядром", "err", err)
		}
		if err := p.CollectOnce(ctx); err != nil {
			p.log().Warn("сбор трафика", "err", err)
		}
	}
}
