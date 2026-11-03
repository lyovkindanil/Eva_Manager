package inbound

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
)

// Link — ссылка vless:// для пользователя с uuid и отображаемым именем
// (подпись после #, например «Данил · NL-1»). Собирается из той же
// типизированной модели, что и конфиг Xray, поэтому они не расходятся.
//
// Формат — как у 3x-ui: type=tcp, security=reality, pbk/sid/sni/fp/flow,
// encryption, support-x25519mlkem768=true (без него Xray 26.9.8+ не
// подтверждает Reality). Такую ссылку разбирает парсер VPN4Eva
// (internal/testutil/vpn4eva) — тесты это проверяют.
func (in Inbound) Link(uuid, name string) (string, error) {
	if uuid == "" {
		return "", fmt.Errorf("инбаунд %q: пустой uuid", in.Name)
	}
	if err := in.Validate(); err != nil {
		return "", fmt.Errorf("инбаунд %q: %w", in.Name, err)
	}
	if in.LinkHost == "" {
		return "", fmt.Errorf("инбаунд %q: не задан адрес для ссылки (link_host)", in.Name)
	}
	r := in.Reality
	pbk, err := r.PublicKey()
	if err != nil {
		return "", fmt.Errorf("инбаунд %q: %w", in.Name, err)
	}
	enc, err := in.encryptionString()
	if err != nil {
		return "", fmt.Errorf("инбаунд %q: %w", in.Name, err)
	}
	q := url.Values{
		"type":       {"tcp"},
		"security":   {"reality"},
		"encryption": {enc},
		"pbk":        {pbk},
		"fp":         {r.Fingerprint},
		"sni":        {r.ServerNames[0]},
		"sid":        {r.ShortIDs[0]},
		"flow":       {r.Flow},
	}
	if r.MLKEM {
		q.Set("support-x25519mlkem768", "true")
	}
	u := &url.URL{
		Scheme:   "vless",
		User:     url.User(uuid),
		Host:     net.JoinHostPort(in.LinkHost, strconv.Itoa(in.Port)),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String(), nil
}
