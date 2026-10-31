package store

import (
	"context"
	"testing"

	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/db/dbtest"
)

func TestEnsureLocalNode(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s := New(d)
		a, err := s.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !a.IsLocal || a.Name != LocalNodeName || len(a.ID) != 32 {
			t.Errorf("узел: %+v", a)
		}
		b, err := s.EnsureLocalNode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if b.ID != a.ID {
			t.Errorf("второй вызов создал новый узел: %s != %s", b.ID, a.ID)
		}
		nodes, err := s.Nodes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 1 || nodes[0].ID != a.ID || !nodes[0].CreatedAt.Equal(a.CreatedAt) {
			t.Errorf("узлы: %+v, ожидался %+v", nodes, a)
		}
	})
}
