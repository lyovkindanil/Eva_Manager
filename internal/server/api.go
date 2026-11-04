package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lyovkindanil/Eva_Manager/internal/awg"
	"github.com/lyovkindanil/Eva_Manager/internal/inbound"
	"github.com/lyovkindanil/Eva_Manager/internal/store"
)

// SettingSubURL — внешний адрес подписок, заданный в интерфейсе
// (EVA_SUB_URL важнее).
const SettingSubURL = "sub_url"

func (s *Server) routes(api *http.ServeMux) {
	api.HandleFunc("GET /api/me", s.me)
	api.HandleFunc("POST /api/logout", s.logout)

	api.HandleFunc("GET /api/users", s.listUsers)
	api.HandleFunc("POST /api/users", s.createUser)
	api.HandleFunc("GET /api/users/{id}", s.getUser)
	api.HandleFunc("PATCH /api/users/{id}", s.patchUser)
	api.HandleFunc("DELETE /api/users/{id}", s.deleteUser)
	api.HandleFunc("POST /api/users/{id}/reset-traffic", s.resetTraffic)
	api.HandleFunc("POST /api/users/{id}/rotate-sub", s.rotateSub)
	api.HandleFunc("GET /api/users/{id}/traffic", s.userTraffic)
	api.HandleFunc("GET /api/users/{id}/awg/{awg}", s.userAWGConf)

	api.HandleFunc("GET /api/inbounds", s.listInbounds)
	api.HandleFunc("POST /api/inbounds", s.createInbound)
	api.HandleFunc("PATCH /api/inbounds/{id}", s.patchInbound)
	api.HandleFunc("DELETE /api/inbounds/{id}", s.deleteInbound)

	api.HandleFunc("GET /api/awg", s.listAWG)
	api.HandleFunc("POST /api/awg", s.createAWG)
	api.HandleFunc("PATCH /api/awg/{id}", s.patchAWG)
	api.HandleFunc("DELETE /api/awg/{id}", s.deleteAWG)

	api.HandleFunc("GET /api/settings", s.getSettings)
	api.HandleFunc("PUT /api/settings", s.putSettings)
	api.HandleFunc("POST /api/apply", s.xrayApply)
}

func (s *Server) node(ctx context.Context) (*store.Node, error) {
	return s.Store.EnsureLocalNode(ctx)
}

// syncNow — изменения пользователей сразу в ядра, не ждать тика сборщика.
// Ошибка не отменяет записанное в базу: сборщик повторит.
func (s *Server) syncNow(ctx context.Context) {
	if s.Panel == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.Panel.SyncUsers(ctx); err != nil {
		s.Log.Warn("сверка с ядрами", "err", err)
	}
}

// applyNow — конфиг Xray и интерфейсы AmneziaWG из базы. Ошибка (конфиг
// отвергнут, откат) уходит клиенту: запись в базе остаётся, её можно
// исправить или удалить.
func (s *Server) applyNow(ctx context.Context) error {
	if s.Panel == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return s.Panel.ApplyConfig(ctx)
}

func (s *Server) userOr404(w http.ResponseWriter, r *http.Request) *store.User {
	u, err := s.Store.UserByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "пользователь не найден")
		return nil
	}
	if err != nil {
		s.fail(w, err)
		return nil
	}
	return u
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.Users(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type userInput struct {
	Name         *string `json:"name"`
	Note         *string `json:"note"`
	TrafficLimit *int64  `json:"traffic_limit"`
	// ExpiresAt — RFC 3339; "" — бессрочно.
	ExpiresAt *string `json:"expires_at"`
	Enabled   *bool   `json:"enabled"`
}

func parseExpires(s *string) (time.Time, error) {
	if s == nil || *s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return time.Time{}, fmt.Errorf("срок: ожидалась дата RFC 3339, получено %q", *s)
	}
	return t, nil
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in userInput
	if !readJSON(w, r, &in) {
		return
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		writeError(w, http.StatusBadRequest, "нужно имя")
		return
	}
	exp, err := parseExpires(in.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var limit int64
	if in.TrafficLimit != nil {
		limit = *in.TrafficLimit
	}
	if limit < 0 {
		writeError(w, http.StatusBadRequest, "лимит не может быть отрицательным")
		return
	}
	note := ""
	if in.Note != nil {
		note = *in.Note
	}
	node, err := s.node(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.Store.UserByName(r.Context(), strings.TrimSpace(*in.Name)); err == nil {
		writeError(w, http.StatusConflict, "пользователь с таким именем уже есть")
		return
	}
	u, err := s.Store.CreateUser(r.Context(), node.ID, strings.TrimSpace(*in.Name), note, limit, exp)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.syncNow(r.Context())
	s.writeUser(w, r, u, http.StatusCreated)
}

// Access — способ подключения пользователя: ссылка vless:// или ключ
// vpn:// AmneziaWG (у него ещё и файл .conf).
type Access struct {
	Kind string `json:"kind"` // vless | awg
	Name string `json:"name"`
	Link string `json:"link"`
	// AWG — id интерфейса для GET /api/users/{id}/awg/{awg}.
	AWG string `json:"awg,omitempty"`
}

// UserView — пользователь со ссылками и адресом подписки.
type UserView struct {
	store.User
	Access []Access `json:"access"`
	SubURL string   `json:"sub_url"`
}

func (s *Server) writeUser(w http.ResponseWriter, r *http.Request, u *store.User, code int) {
	ctx := r.Context()
	v := UserView{User: *u, Access: []Access{}}
	inbounds, err := s.Store.UserInbounds(ctx, u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, in := range inbounds {
		link, err := in.Link(u.UUID, u.Name+" · "+in.Name)
		if err != nil {
			s.fail(w, err)
			return
		}
		v.Access = append(v.Access, Access{Kind: "vless", Name: in.Name, Link: link})
	}
	awgs, err := s.Store.UserAWGs(ctx, u.ID)
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
		v.Access = append(v.Access, Access{Kind: "awg", Name: a.Interface.Name, Link: key, AWG: a.Interface.ID})
	}
	base, err := s.subBase(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.SubURL = base + "/sub/" + u.SubToken
	writeJSON(w, code, v)
}

// subBase — внешний адрес подписок: EVA_SUB_URL, иначе из настроек, иначе
// адрес, по которому открыта панель (с портом EVA_SUB_LISTEN, если он
// задан).
func (s *Server) subBase(r *http.Request) (string, error) {
	if s.SubURL != "" {
		return s.SubURL, nil
	}
	v, err := s.Store.Setting(r.Context(), SettingSubURL)
	if err != nil || v != "" {
		return v, err
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if s.SubListen != "" {
		if _, port, err := net.SplitHostPort(s.SubListen); err == nil {
			h := host
			if hh, _, err := net.SplitHostPort(host); err == nil {
				h = hh
			}
			host = net.JoinHostPort(strings.Trim(h, "[]"), port)
		}
	}
	return scheme + "://" + host, nil
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	if u := s.userOr404(w, r); u != nil {
		s.writeUser(w, r, u, http.StatusOK)
	}
}

func (s *Server) patchUser(w http.ResponseWriter, r *http.Request) {
	u := s.userOr404(w, r)
	if u == nil {
		return
	}
	var in userInput
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	if in.Name != nil && *in.Name != u.Name {
		writeError(w, http.StatusBadRequest, "имя не меняется: оно в подписях ссылок у клиентов")
		return
	}
	if in.Note != nil {
		if err := s.Store.SetUserNote(ctx, u.ID, *in.Note); err != nil {
			s.fail(w, err)
			return
		}
	}
	if in.TrafficLimit != nil || in.ExpiresAt != nil {
		limit, exp := u.TrafficLimit, u.ExpiresAt
		if in.TrafficLimit != nil {
			limit = *in.TrafficLimit
		}
		if in.ExpiresAt != nil {
			var err error
			if exp, err = parseExpires(in.ExpiresAt); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if limit < 0 {
			writeError(w, http.StatusBadRequest, "лимит не может быть отрицательным")
			return
		}
		if err := s.Store.SetUserLimits(ctx, u.ID, limit, exp); err != nil {
			s.fail(w, err)
			return
		}
	}
	if in.Enabled != nil {
		if err := s.Store.SetUserDisabled(ctx, u.ID, !*in.Enabled); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.refreshStatus(ctx, u.ID)
	s.syncNow(ctx)
	if u = s.userOr404(w, r); u != nil {
		s.writeUser(w, r, u, http.StatusOK)
	}
}

// refreshStatus пересчитывает статус сразу (продление возвращает
// пользователя без ожидания тика).
func (s *Server) refreshStatus(ctx context.Context, id string) {
	u, err := s.Store.UserByID(ctx, id)
	if err != nil {
		return
	}
	if st := u.ComputeStatus(time.Now()); st != u.Status {
		if err := s.Store.SetUserStatus(ctx, id, st); err != nil {
			s.Log.Warn("статус пользователя", "err", err)
		}
	}
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u := s.userOr404(w, r)
	if u == nil {
		return
	}
	if err := s.Store.DeleteUser(r.Context(), u.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.syncNow(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) resetTraffic(w http.ResponseWriter, r *http.Request) {
	u := s.userOr404(w, r)
	if u == nil {
		return
	}
	if err := s.Store.ResetUserTraffic(r.Context(), u.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.refreshStatus(r.Context(), u.ID)
	s.syncNow(r.Context())
	s.getUser(w, r)
}

func (s *Server) rotateSub(w http.ResponseWriter, r *http.Request) {
	u := s.userOr404(w, r)
	if u == nil {
		return
	}
	if _, err := s.Store.RotateSubToken(r.Context(), u.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.getUser(w, r)
}

func (s *Server) userTraffic(w http.ResponseWriter, r *http.Request) {
	u := s.userOr404(w, r)
	if u == nil {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 366 {
		days = 30
	}
	t, err := s.Store.UserTrafficDaily(r.Context(), u.ID, days)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// userAWGConf — файл .conf пользователя для приложений AmneziaVPN и
// AmneziaWG (скачивание).
func (s *Server) userAWGConf(w http.ResponseWriter, r *http.Request) {
	u := s.userOr404(w, r)
	if u == nil {
		return
	}
	awgs, err := s.Store.UserAWGs(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, a := range awgs {
		if a.Interface.ID != r.PathValue("awg") {
			continue
		}
		conf, err := a.Interface.ClientConf(a.Peer, u.Name+" · "+a.Interface.Name)
		if err != nil {
			s.fail(w, err)
			return
		}
		// Имя файла становится именем туннеля в AmneziaWG: латиница.
		file := asciiName(u.Name + "-" + a.Interface.Iface)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.conf"; filename*=UTF-8''%s.conf`,
			file, url.PathEscape(u.Name+"-"+a.Interface.Iface)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(conf))
		return
	}
	writeError(w, http.StatusNotFound, "у пользователя нет этого интерфейса AmneziaWG")
}

func asciiName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() > 15 { // предел имени интерфейса у wg-quick
		return b.String()[:15]
	}
	return b.String()
}

// InboundView — инбаунд без приватных ключей.
type InboundView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Port        int    `json:"port"`
	Host        string `json:"host"`
	Enabled     bool   `json:"enabled"`
	Dest        string `json:"dest"`
	PublicKey   string `json:"public_key"`
	ShortID     string `json:"short_id"`
	Encryption  bool   `json:"encryption"`
	Fingerprint string `json:"fingerprint"`
}

func inboundView(in inbound.Inbound) InboundView {
	pbk, _ := in.Reality.PublicKey()
	v := InboundView{ID: in.ID, Name: in.Name, Protocol: in.Protocol, Port: in.Port, Host: in.LinkHost, Enabled: in.Enabled,
		Dest: in.Reality.Dest, PublicKey: pbk, Encryption: in.Reality.Encryption != nil, Fingerprint: in.Reality.Fingerprint}
	if len(in.Reality.ShortIDs) > 0 {
		v.ShortID = in.Reality.ShortIDs[0]
	}
	return v
}

func (s *Server) listInbounds(w http.ResponseWriter, r *http.Request) {
	node, err := s.node(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	list, err := s.Store.Inbounds(r.Context(), node.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []InboundView{}
	for _, in := range list {
		out = append(out, inboundView(in))
	}
	writeJSON(w, http.StatusOK, out)
}

type inboundInput struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Dest       string `json:"dest"`
	Encryption bool   `json:"encryption"`
}

// applyResult — ответ на изменение инбаунда или AmneziaWG: запись в базе
// есть всегда, applied — дошло ли до ядра (иначе error объясняет).
type applyResult struct {
	Item    any    `json:"item,omitempty"`
	Applied bool   `json:"applied"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) applied(w http.ResponseWriter, r *http.Request, item any, code int) {
	res := applyResult{Item: item, Applied: s.Panel != nil}
	if err := s.applyNow(r.Context()); err != nil {
		res.Applied, res.Error = false, err.Error()
	}
	writeJSON(w, code, res)
}

func (s *Server) createInbound(w http.ResponseWriter, r *http.Request) {
	var in inboundInput
	if !readJSON(w, r, &in) {
		return
	}
	node, err := s.node(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if in.Port == 0 {
		in.Port = inbound.DefaultPort
	}
	ib, err := inbound.NewVLESSReality(store.NewID(), node.ID, strings.TrimSpace(in.Name), in.Port, strings.TrimSpace(in.Host), strings.TrimSpace(in.Dest))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Encryption {
		if err := ib.EnableEncryption("native", "0rtt", 600, ""); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := s.portFree(r.Context(), node.ID, "tcp", ib.Port, ""); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.CreateInbound(r.Context(), ib); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.applied(w, r, inboundView(ib), http.StatusCreated)
}

// portFree — порт не занят другим инбаундом узла (TCP) или интерфейсом
// AmneziaWG (UDP). Занятость порта чужой программой покажет проверка
// конфига или запуск процесса.
func (s *Server) portFree(ctx context.Context, nodeID, proto string, port int, except string) error {
	if proto == "tcp" {
		list, err := s.Store.Inbounds(ctx, nodeID)
		if err != nil {
			return err
		}
		for _, in := range list {
			if in.Port == port && in.ID != except {
				return fmt.Errorf("порт %d уже у инбаунда %q", port, in.Name)
			}
		}
		return nil
	}
	list, err := s.Store.AWGInterfaces(ctx, nodeID)
	if err != nil {
		return err
	}
	for _, in := range list {
		if in.Port == port && in.ID != except {
			return fmt.Errorf("UDP-порт %d уже у AmneziaWG %q", port, in.Name)
		}
	}
	return nil
}

type enabledInput struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) patchInbound(w http.ResponseWriter, r *http.Request) {
	in, err := s.Store.InboundByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "инбаунд не найден")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	var p enabledInput
	if !readJSON(w, r, &p) {
		return
	}
	if p.Enabled != nil {
		if err := s.Store.SetInboundEnabled(r.Context(), in.ID, *p.Enabled); err != nil {
			s.fail(w, err)
			return
		}
		in.Enabled = *p.Enabled
	}
	s.applied(w, r, inboundView(*in), http.StatusOK)
}

func (s *Server) deleteInbound(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteInbound(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.applied(w, r, nil, http.StatusOK)
}

// AWGView — интерфейс AmneziaWG без приватного ключа.
type AWGView struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Iface     string          `json:"iface"`
	Port      int             `json:"port"`
	Host      string          `json:"host"`
	Enabled   bool            `json:"enabled"`
	Subnet    string          `json:"subnet"`
	MTU       int             `json:"mtu"`
	DNS       string          `json:"dns"`
	PublicKey string          `json:"public_key"`
	Obf       awg.Obfuscation `json:"obfuscation"`
	Running   bool            `json:"running"`
	Problem   string          `json:"problem,omitempty"`
}

func (s *Server) awgView(in awg.Interface) AWGView {
	pub, _ := awg.PublicKey(in.Settings.PrivateKey)
	v := AWGView{ID: in.ID, Name: in.Name, Iface: in.Iface, Port: in.Port, Host: in.LinkHost, Enabled: in.Enabled,
		Subnet: in.Settings.Subnet, MTU: in.Settings.MTU, DNS: in.Settings.DNS, PublicKey: pub, Obf: in.Settings.Obf}
	if s.AWG != nil {
		for _, st := range s.AWG() {
			if st.ID == in.ID {
				v.Running, v.Problem = st.Running, st.Problem
			}
		}
	}
	return v
}

func (s *Server) listAWG(w http.ResponseWriter, r *http.Request) {
	node, err := s.node(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	list, err := s.Store.AWGInterfaces(r.Context(), node.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []AWGView{}
	for _, in := range list {
		out = append(out, s.awgView(in))
	}
	writeJSON(w, http.StatusOK, out)
}

type awgInput struct {
	Name   string `json:"name"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Subnet string `json:"subnet"`
	DNS    string `json:"dns"`
}

func (s *Server) createAWG(w http.ResponseWriter, r *http.Request) {
	var in awgInput
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	node, err := s.node(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	existing, err := s.Store.AWGInterfaces(ctx, node.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	a, err := awg.NewOnNode(store.NewID(), node.ID, awg.Options{Name: strings.TrimSpace(in.Name), Host: strings.TrimSpace(in.Host),
		Port: in.Port, Subnet: strings.TrimSpace(in.Subnet), DNS: strings.TrimSpace(in.DNS)}, existing)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.portFree(ctx, node.ID, "udp", a.Port, ""); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.Store.CreateAWG(ctx, a); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.syncNow(ctx)
	writeJSON(w, http.StatusCreated, s.awgView(a))
}

func (s *Server) awgOr404(w http.ResponseWriter, r *http.Request) *awg.Interface {
	node, err := s.node(r.Context())
	if err != nil {
		s.fail(w, err)
		return nil
	}
	list, err := s.Store.AWGInterfaces(r.Context(), node.ID)
	if err != nil {
		s.fail(w, err)
		return nil
	}
	for _, in := range list {
		if in.ID == r.PathValue("id") {
			return &in
		}
	}
	writeError(w, http.StatusNotFound, "интерфейс AmneziaWG не найден")
	return nil
}

func (s *Server) patchAWG(w http.ResponseWriter, r *http.Request) {
	in := s.awgOr404(w, r)
	if in == nil {
		return
	}
	var p enabledInput
	if !readJSON(w, r, &p) {
		return
	}
	if p.Enabled != nil {
		if err := s.Store.SetAWGEnabled(r.Context(), in.ID, *p.Enabled); err != nil {
			s.fail(w, err)
			return
		}
		in.Enabled = *p.Enabled
	}
	s.syncNow(r.Context())
	writeJSON(w, http.StatusOK, s.awgView(*in))
}

func (s *Server) deleteAWG(w http.ResponseWriter, r *http.Request) {
	in := s.awgOr404(w, r)
	if in == nil {
		return
	}
	if err := s.Store.DeleteAWG(r.Context(), in.ID); err != nil {
		s.fail(w, err)
		return
	}
	s.syncNow(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Settings — настройки панели из интерфейса.
type Settings struct {
	SubURL string `json:"sub_url"`
	// SubURLFromEnv — адрес задан в EVA_SUB_URL и в интерфейсе не меняется.
	SubURLFromEnv bool `json:"sub_url_from_env"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.Setting(r.Context(), SettingSubURL)
	if err != nil {
		s.fail(w, err)
		return
	}
	st := Settings{SubURL: v}
	if s.SubURL != "" {
		st = Settings{SubURL: s.SubURL, SubURLFromEnv: true}
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if !readJSON(w, r, &in) {
		return
	}
	if s.SubURL != "" && in.SubURL != s.SubURL {
		writeError(w, http.StatusConflict, "адрес подписок задан в EVA_SUB_URL")
		return
	}
	v := strings.TrimRight(strings.TrimSpace(in.SubURL), "/")
	if v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "адрес подписок: ожидался вид https://vpn.example.com:2096")
			return
		}
	}
	if s.SubURL == "" {
		if err := s.Store.SetSetting(r.Context(), SettingSubURL, v); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.getSettings(w, r)
}
