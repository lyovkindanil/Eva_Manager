// Сгенерировано scripts/sync-vpn4eva-parser.sh — не править вручную.
// Источник: github.com/lyovkindanil/Vpn4Eva, коммит 7a5ab396a8731568ccf22b736898b8fcebca3b7a:
//   core/internal/profile/profile.go (только parsePrefix)

package profile

import (
	"net/netip"
	"strings"
)

// parsePrefix принимает как подсеть, так и одиночный адрес.
func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		return netip.ParsePrefix(s)
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}
