package panel

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
)

// fakeCore записывает применённые конфиги и умеет отказывать.
type fakeCore struct {
	applied [][]byte
	err     error
}

func (f *fakeCore) Apply(_ context.Context, config []byte) error {
	if f.err != nil {
		return f.err
	}
	f.applied = append(f.applied, config)
	return nil
}

// ApplyConfig: конфиг из базы уходит в ядро, попытки пишутся в историю
// версий, при успехе пользователи сверяются с новым ядром.
func TestApplyConfig(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s, node, in, api, p := setup(t, d)
		core := &fakeCore{}
		p.Core = core
		p.APIAddr = "127.0.0.1:10085"

		// Ядром панель не управляет — понятная ошибка, версий нет.
		if err := (&Panel{Store: s, NodeID: node.ID}).ApplyConfig(ctx); err == nil {
			t.Error("без Core: ожидалась ошибка")
		}
		if versions, _ := s.ConfigVersions(ctx, node.ID, 0); len(versions) != 0 {
			t.Errorf("версии без применения: %v", versions)
		}

		// Успех: конфиг с инбаундом ушёл в ядро, версия TestOK,
		// пользователь появился в новом ядре.
		u, err := s.CreateUser(ctx, node.ID, "danil", "", 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if err := p.ApplyConfig(ctx); err != nil {
			t.Fatal(err)
		}
		if len(core.applied) != 1 || !strings.Contains(string(core.applied[0]), in.Tag()) {
			t.Fatalf("применённый конфиг: %v", core.applied)
		}
		versions, err := s.ConfigVersions(ctx, node.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(versions) != 1 || !versions[0].TestOK || versions[0].Version != 1 {
			t.Fatalf("версии: %+v", versions)
		}
		if got := api.inCore(in.Tag()); !slices.Equal(got, []string{u.ID}) {
			t.Errorf("в ядре после применения: %v", got)
		}

		// Отказ ядра: ошибка наружу, версия с TestOK=false и выводом,
		// рабочая версия на месте.
		core.err = errors.New("xray отверг конфиг: bad port")
		err = p.ApplyConfig(ctx)
		if err == nil || !strings.Contains(err.Error(), "bad port") {
			t.Fatalf("ошибка применения: %v", err)
		}
		versions, err = s.ConfigVersions(ctx, node.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(versions) != 2 {
			t.Fatalf("версии после отказа: %+v", versions)
		}
		if versions[0].TestOK || !strings.Contains(versions[0].TestOutput, "bad port") {
			t.Errorf("отвергнутая версия: %+v", versions[0])
		}
		if !versions[1].TestOK {
			t.Errorf("рабочая версия потерялась: %+v", versions[1])
		}
	})
}
