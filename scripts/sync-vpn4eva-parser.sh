#!/bin/sh
# Обновляет копии парсера серверов (vless://, .conf и vpn://) из клиента VPN4Eva, которыми
# тесты панели проверяют сгенерированные ссылки (internal/testutil/vpn4eva).
#
#   scripts/sync-vpn4eva-parser.sh [коммит]
#
# По умолчанию обновляет до коммита, закреплённого в заголовках копий;
# чтобы поднять версию парсера, передайте новый коммит явно.
# Репозиторий VPN4Eva приватный. Источник, в порядке приоритета:
#   1. локальный клон из VPN4EVA_SRC (не переключается, читаем git show);
#   2. временное частичное скачивание с GitHub (нужны учётные данные git
#      с доступом к репозиторию).
#
# Что копируется:
#   - core/internal/profile/{server,wireguard,amnezia,hysteria2}.go —
#     целиком: разбор сервера любого протокола, в том числе .conf
#     AmneziaWG и ключей vpn://;
#   - из core/internal/profile/profile.go — только parsePrefix;
#   - core/internal/profile/vless.go — целиком, с переписанным импортом
#     vlessenc (в апстриме sing-box этого пакета нет, VPN4Eva собирает
#     его своими патчами, поэтому оригинальный путь недоступен панели);
#   - из patches/sing-box/common/vlessenc/vpn4eva.go — только Config и
#     ParseClient (разбор строки encryption): этого достаточно парсеру
#     ссылок, а передача данных панели не нужна. Так копиям не требуются
#     зависимости sing-vmess/sing.
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
dst="$here/internal/testutil/vpn4eva"
commit=${1:-$(sed -n 's|.*коммит \([0-9a-f]\{40\}\).*|\1|p' "$dst/profile/vless.go" | head -n1)}
if [ -z "$commit" ]; then
	echo "sync-vpn4eva-parser: не нашёл закреплённый коммит в $dst/profile/vless.go" >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if [ -n "${VPN4EVA_SRC:-}" ]; then
	src=$VPN4EVA_SRC
else
	src="$tmp/vpn4eva"
	git clone -q --filter=blob:none --no-checkout https://github.com/lyovkindanil/Vpn4Eva "$src"
	git -C "$src" fetch -q --depth 1 origin "$commit"
fi

hdr() { # путь исходника в репозитории VPN4Eva
	printf '// Сгенерировано scripts/sync-vpn4eva-parser.sh — не править вручную.\n'
	printf '// Источник: github.com/lyovkindanil/Vpn4Eva, коммит %s:\n' "$commit"
	printf '//   %s\n\n' "$1"
}

# Парсер ссылок, целиком; импорт vlessenc переписан на локальную копию.
{
	hdr "core/internal/profile/vless.go (импорт vlessenc переписан на локальную копию)"
	git -C "$src" show "$commit:core/internal/profile/vless.go" |
		sed 's|github.com/sagernet/sing-box/common/vlessenc|github.com/lyovkindanil/Eva_Manager/internal/testutil/vpn4eva/vlessenc|'
} >"$dst/profile/vless.go"

for f in server wireguard amnezia hysteria2; do
	{
		hdr "core/internal/profile/$f.go"
		git -C "$src" show "$commit:core/internal/profile/$f.go"
	} >"$dst/profile/$f.go"
done
# Из profile.go — только parsePrefix (остальное — профиль клиента).
{
	hdr "core/internal/profile/profile.go (только parsePrefix)"
	printf 'package profile\n\nimport (\n\t"net/netip"\n\t"strings"\n)\n\n'
	git -C "$src" show "$commit:core/internal/profile/profile.go" |
		awk '/^\/\/ parsePrefix /{f=1} f; f && /^}/{f=0}'
} >"$dst/profile/prefix.go"

# vlessenc: только разбор строки encryption (Config и ParseClient) —
# от комментария «// Config —» до комментария «// NewClient».
{
	hdr "patches/sing-box/common/vlessenc/vpn4eva.go (только Config и ParseClient: полная реализация передачи данных панели не нужна)"
	printf 'package vlessenc\n\nimport (\n\t"encoding/base64"\n\t"errors"\n\t"strings"\n)\n\n'
	git -C "$src" show "$commit:patches/sing-box/common/vlessenc/vpn4eva.go" |
		awk '/^\/\/ Config —/{f=1} /^\/\/ NewClient/{f=0} f'
} >"$dst/vlessenc/vlessenc.go"
gofmt -w "$dst/vlessenc/vlessenc.go"
rm -f "$dst/vlessenc/client.go" "$dst/vlessenc/common.go" "$dst/vlessenc/server.go" "$dst/vlessenc/vpn4eva.go" "$dst/vlessenc/xor.go"

if ! git -C "$here" diff --quiet --exit-code -- "$dst" 2>/dev/null; then
	echo "sync-vpn4eva-parser: копии обновлены до $commit"
else
	echo "sync-vpn4eva-parser: копии уже на $commit"
fi
