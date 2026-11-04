package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

// subUpdateInterval — как часто клиент обновляет подписку, часов
// (заголовок profile-update-interval, его читает VPN4Eva).
const subUpdateInterval = 12

// subscription — GET /sub/<токен>: ссылки пользователя в формате v2rayN
// (base64 от списка ссылок построчно; AmneziaWG — ключами vpn://) плюс заголовки, которые показывает
// VPN4Eva: profile-title (кириллица — в форме base64:…),
// subscription-userinfo (трафик и срок) и profile-update-interval.
//
// Токен — 128 случайных бит, поэтому авторизации на маршруте нет: сам
// адрес и есть секрет (перевыпуск токена — этап 3). Кэш запрещён:
// клиент должен видеть свежие трафик и срок.
func (s *Server) subscription(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.UserBySubToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "подписка не найдена")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	inbounds, err := s.Store.UserInbounds(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	links := []string{}
	for _, in := range inbounds {
		link, err := in.Link(u.UUID, u.Name+" · "+in.Name)
		if err != nil {
			s.fail(w, err)
			return
		}
		links = append(links, link)
	}
	// AmneziaWG — ключами vpn:// (формат AmneziaVPN): их понимают VPN4Eva
	// и приложение AmneziaVPN, остальные клиенты такие строки пропускают.
	awgs, err := s.Store.UserAWGs(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, a := range awgs {
		key, err := a.Interface.VPNKey(a.Peer, u.Name+" · "+a.Interface.Name)
		if err != nil {
			s.fail(w, err)
			return
		}
		links = append(links, key)
	}

	h := w.Header()
	// Заголовки HTTP не несут UTF-8: кириллицу отдаём в base64-форме,
	// как договорились с клиентом (см. план, раздел подписок).
	if isASCII(u.Name) {
		h.Set("Profile-Title", u.Name)
	} else {
		h.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(u.Name)))
	}
	// upload/download по факту не разделяем (лимит считает сумму):
	// весь расход уходит в download — так показывает и 3x-ui.
	var expire int64
	if !u.ExpiresAt.IsZero() {
		expire = u.ExpiresAt.Unix()
	}
	h.Set("Subscription-Userinfo", fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d",
		u.TrafficUsed, u.TrafficLimit, expire))
	h.Set("Profile-Update-Interval", fmt.Sprint(subUpdateInterval))
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
