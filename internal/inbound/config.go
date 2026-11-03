package inbound

import (
	"encoding/json"
	"fmt"

	"github.com/lyovkindanil/Eva_Manager/internal/xray"
)

// ConfigFragment — инбаунд в формате конфига Xray. Пользователи
// добавляются в работающее ядро через gRPC API (HandlerService),
// поэтому список clients в конфиге пуст.
func (in Inbound) ConfigFragment() (map[string]any, error) {
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("инбаунд %q: %w", in.Name, err)
	}
	r := in.Reality
	return map[string]any{
		"tag":      in.Tag(),
		"listen":   "0.0.0.0",
		"port":     in.Port,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{},
			"decryption": in.decryptionString(),
		},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"show":        false,
				"dest":        r.Dest,
				"serverNames": r.ServerNames,
				"privateKey":  r.PrivateKey,
				"shortIds":    r.ShortIDs,
			},
		},
		"sniffing": map[string]any{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
			"routeOnly":    true,
		},
	}, nil
}

// BuildConfig — полный конфиг Xray: базовый (gRPC API, статистика, выход,
// блокировка частных сетей) плюс включённые инбаунды.
func BuildConfig(apiAddr string, inbounds []Inbound) ([]byte, error) {
	base, err := xray.BaseConfig(apiAddr)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(base, &cfg); err != nil {
		return nil, fmt.Errorf("базовый конфиг: %w", err)
	}
	list, _ := cfg["inbounds"].([]any)
	for _, in := range inbounds {
		if !in.Enabled {
			continue
		}
		frag, err := in.ConfigFragment()
		if err != nil {
			return nil, err
		}
		list = append(list, frag)
	}
	cfg["inbounds"] = list
	return json.MarshalIndent(cfg, "", "  ")
}
