package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
)

func TestConfigVersions(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s := New(d)
		node, err := s.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}

		// Номера версий растут от 1, сохраняется всё как передано.
		v1, err := s.SaveConfigVersion(ctx, node.ID, `{"a":1}`, true, "")
		if err != nil {
			t.Fatal(err)
		}
		v2, err := s.SaveConfigVersion(ctx, node.ID, `{"a":2}`, false, "xray: bad config")
		if err != nil {
			t.Fatal(err)
		}
		if v1 != 1 || v2 != 2 {
			t.Errorf("версии: %d, %d, ожидались 1, 2", v1, v2)
		}

		list, err := s.ConfigVersions(ctx, node.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 || list[0].Version != 2 || list[1].Version != 1 {
			t.Fatalf("порядок версий: %+v", list)
		}
		if list[0].Config != `{"a":2}` || list[0].TestOK || list[0].TestOutput != "xray: bad config" {
			t.Errorf("вторая версия: %+v", list[0])
		}
		if !list[0].CreatedAt.After(list[1].CreatedAt.Add(-time.Second)) {
			t.Errorf("даты: %v и %v", list[0].CreatedAt, list[1].CreatedAt)
		}

		// Отдельная версия по номеру и ошибка на несуществующую.
		got, err := s.ConfigVersionByVersion(ctx, node.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got.Config != `{"a":1}` || !got.TestOK {
			t.Errorf("первая версия: %+v", got)
		}
		if _, err := s.ConfigVersionByVersion(ctx, node.ID, 99); err != ErrNotFound {
			t.Errorf("несуществующая версия: %v, ожидалась ErrNotFound", err)
		}

		// Храним не больше ConfigVersionsKeep последних версий.
		for i := 0; i < ConfigVersionsKeep; i++ {
			if _, err := s.SaveConfigVersion(ctx, node.ID, fmt.Sprintf(`{"n":%d}`, i), true, ""); err != nil {
				t.Fatal(err)
			}
		}
		list, err = s.ConfigVersions(ctx, node.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != ConfigVersionsKeep {
			t.Fatalf("после чистки версий %d, ожидалось %d", len(list), ConfigVersionsKeep)
		}
		last := 2 + ConfigVersionsKeep
		if list[0].Version != int64(last) || list[len(list)-1].Version != int64(last-ConfigVersionsKeep+1) {
			t.Errorf("диапазон версий: %d..%d, ожидался %d..%d",
				list[len(list)-1].Version, list[0].Version, last-ConfigVersionsKeep+1, last)
		}

		// limit режет выдачу.
		list, err = s.ConfigVersions(ctx, node.ID, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 3 {
			t.Errorf("с limit=3 версий %d", len(list))
		}

		// Версии другого узла не смешиваются.
		if _, err := s.SaveConfigVersion(ctx, "другой-узел", `{}`, true, ""); err == nil {
			t.Error("несуществующий узел: ожидалась ошибка внешнего ключа")
		}
	})
}
