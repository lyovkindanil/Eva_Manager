package main

import (
	"context"
	"fmt"

	"github.com/lyovkindanil/Eva_Manager/internal/config"
	"github.com/lyovkindanil/Eva_Manager/internal/db"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

// openStore открывает базу и возвращает Store, локальный узел и функцию
// закрытия — общее начало всех CLI-команд.
func openStore(ctx context.Context, cfg *config.Config) (*store.Store, *store.Node, func(), error) {
	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	st := store.New(d)
	node, err := st.EnsureLocalNode(ctx)
	if err != nil {
		d.Close()
		return nil, nil, nil, err
	}
	return st, node, func() { d.Close() }, nil
}

// humanBytes — байты человеком: 12.3 ГБ.
func humanBytes(n int64) string {
	const unit = 1 << 30
	if n >= unit {
		return fmt.Sprintf("%.1f ГБ", float64(n)/unit)
	}
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d Б", n)
}
