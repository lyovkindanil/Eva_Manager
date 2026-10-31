// Package xray запускает Xray-core как дочерний процесс, проверяет его
// конфиг командой `xray run -test` и ходит в его gRPC API.
package xray

import (
	"encoding/json"
	"net"
)

// APITag — тег inbound и outbound служебного API в конфиге.
const APITag = "api"

// privateNets — частные, служебные и локальные сети IPv4 и IPv6.
var privateNets = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
}

// BaseConfig — минимальный рабочий конфиг: только gRPC API на apiAddr
// (127.0.0.1:порт), сбор статистики по пользователям и прямой выход.
// Инбаунды для пользователей добавляет сборщик конфига (этап 1).
//
// Статистика по пользователю появляется в Xray, только если у пользователя
// задан email и уровень, у которого в policy включены statsUserUplink и
// statsUserDownlink; поэтому уровень 0 включает их сразу.
func BaseConfig(apiAddr string) ([]byte, error) {
	if _, _, err := net.SplitHostPort(apiAddr); err != nil {
		return nil, err
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"api": map[string]any{
			"tag":      APITag,
			"listen":   apiAddr,
			"services": []string{"HandlerService", "StatsService", "LoggerService"},
		},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{
				"0": map[string]any{"statsUserUplink": true, "statsUserDownlink": true, "statsUserOnline": true},
			},
			"system": map[string]any{
				"statsInboundUplink":    true,
				"statsInboundDownlink":  true,
				"statsOutboundUplink":   true,
				"statsOutboundDownlink": true,
			},
		},
		"inbounds": []any{},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "block", "protocol": "blackhole"},
		},
		"routing": map[string]any{
			"rules": []any{
				// Частные сети из туннеля не открываем. Списком, а не
				// geoip:private: базовый конфиг не зависит от geoip.dat.
				map[string]any{"type": "field", "ip": privateNets, "outboundTag": "block"},
			},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}
