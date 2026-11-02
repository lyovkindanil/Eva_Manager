package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

// applyCmd просит работающую панель собрать конфиг из базы и применить
// его к ядру (проверка и откат — на стороне панели).
func applyCmd(ctx context.Context, cfg *config.Config) error {
	url := "http://" + cfg.Listen + "/api/xray/apply"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("панель не отвечает (запущена?): %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("конфиг не применён: %s", strings.TrimSpace(string(body)))
	}
	fmt.Println("Конфиг из базы применён к работающему ядру.")
	return nil
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
