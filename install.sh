#!/bin/sh
# Установка и обновление Eva Manager на Linux с systemd (Ubuntu, Debian и
# другие). Запускать от root:
#
#   curl -fsSL https://raw.githubusercontent.com/lyovkindanil/Eva_Manager/main/install.sh | sudo sh
#
# Параметры (после `sh -s --`, если скрипт идёт через curl):
#   --version 1.2.3   версия выпуска (по умолчанию — последняя; или EVA_VERSION)
#   --file путь.tar.gz  поставить из скачанного архива выпуска
#   --listen адрес    адрес панели для нового файла настроек (по умолчанию 127.0.0.1:8080)
#   --sub-port порт   порт подписок для нового файла настроек (по умолчанию 2096)
#   --yes             не спрашивать: если стандартные порты заняты, взять свободные
#   --no-start        не запускать службу
#   --uninstall       удалить программу и службу; данные остаются
#
# Повторный запуск обновляет программу: данные (/var/lib/eva-manager) и
# настройки (/etc/eva-manager/env) не трогаются.
set -eu

repo=lyovkindanil/Eva_Manager
lib=/usr/local/lib/eva-manager
data=/var/lib/eva-manager
etc=/etc/eva-manager
unit=/etc/systemd/system/eva-manager.service

version=${EVA_VERSION:-}
file=
listen=127.0.0.1:8080
sub_port=2096
yes=0
start=1
uninstall=0

say() { printf '%s\n' "$*"; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) version=${2:?--version: нужна версия}; shift 2 ;;
	--file) file=${2:?--file: нужен путь к архиву}; shift 2 ;;
	--listen) listen=${2:?--listen: нужен адрес}; shift 2 ;;
	--sub-port) sub_port=${2:?--sub-port: нужен порт}; shift 2 ;;
	--yes | -y) yes=1; shift ;;
	--no-start) start=0; shift ;;
	--uninstall) uninstall=1; shift ;;
	-h | --help) sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) die "неизвестный параметр $1 (см. --help)" ;;
	esac
done
version=${version#v}

[ "$(id -u)" = 0 ] || die "нужен root: sudo sh install.sh"
[ "$(uname -s)" = Linux ] || die "нужен Linux"
command -v systemctl >/dev/null 2>&1 || die "нужен systemd (для Docker есть docker-compose.yml)"

if [ "$uninstall" = 1 ]; then
	systemctl disable --now eva-manager 2>/dev/null || true
	rm -f "$unit" /usr/local/bin/eva-manager /etc/sysctl.d/99-eva-manager.conf
	rm -rf "$lib"
	systemctl daemon-reload
	say "Eva Manager удалён. Данные и настройки остались в $data и $etc —"
	say "удалите их вручную, если они больше не нужны (userdel eva — пользователь)."
	exit 0
fi

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "архитектура $(uname -m) не поддерживается (нужна amd64 или arm64)" ;;
esac

# Занятые порты. Сначала ss, без него — /proc/net (порт в hex, для TCP
# только слушающие сокеты).
port_busy() { # tcp|udp порт
	if command -v ss >/dev/null 2>&1; then
		if [ "$1" = tcp ]; then
			[ -n "$(ss -Hltn "sport = :$2" 2>/dev/null)" ]
		else
			[ -n "$(ss -Hlun "sport = :$2" 2>/dev/null)" ]
		fi
		return
	fi
	hex=$(printf ':%04X' "$2")
	if [ "$1" = tcp ]; then
		cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk -v h="$hex" '$4 == "0A" && substr($2, length($2) - 4) == h { f = 1 } END { exit !f }'
	else
		cat /proc/net/udp /proc/net/udp6 2>/dev/null | awk -v h="$hex" 'substr($2, length($2) - 4) == h { f = 1 } END { exit !f }'
	fi
}

# Кто занял порт — для сообщения: «2096 (x-ui)».
port_owner() { # tcp|udp порт
	command -v ss >/dev/null 2>&1 || return 0
	if [ "$1" = tcp ]; then o=$(ss -Hltnp "sport = :$2" 2>/dev/null); else o=$(ss -Hlunp "sport = :$2" 2>/dev/null); fi
	printf '%s' "$o" | sed -n 's/.*users:(("\([^"]*\)".*/\1/p' | head -n1
}

describe() { # tcp|udp порт
	who=$(port_owner "$1" "$2")
	if [ -n "$who" ]; then printf '%s/%s (%s)' "$2" "$1" "$who"; else printf '%s/%s' "$2" "$1"; fi
}

free_port() { # tcp порт [занятый] — первый свободный от порта, кроме занятого
	p=$1
	while port_busy tcp "$p" || [ "$p" = "${2:-}" ]; do p=$((p + 1)); done
	echo "$p"
}

# Да или нет с терминала; через curl | sh стандартный ввод занят скриптом,
# поэтому читаем /dev/tty. Без терминала — ответ по умолчанию.
ask_yes() { # вопрос
	[ "$yes" = 1 ] && return 0
	if ! (exec </dev/tty) 2>/dev/null; then
		say "$1 — нет терминала для вопроса, беру свободные (или запустите с --listen / --sub-port)."
		return 0
	fi
	printf '%s [Y/n] ' "$1" >/dev/tty
	read -r ans </dev/tty || ans=
	case "$ans" in [nNнН]*) return 1 ;; *) return 0 ;; esac
}

# Новая установка: панель и подписки не должны столкнуться с другой
# панелью (3x-ui держит подписки на 2096, Hiddify — 443) или wg-easy.
if [ ! -f "$etc/env" ]; then
	listen_host=${listen%:*}
	listen_port=${listen##*:}
	busy=
	port_busy tcp "$listen_port" && busy="$busy $(describe tcp "$listen_port")"
	port_busy tcp "$sub_port" && busy="$busy $(describe tcp "$sub_port")"
	if [ -n "$busy" ]; then
		say "Стандартные порты заняты:$busy"
		if ask_yes "Использовать свободные порты?"; then
			listen_port=$(free_port "$listen_port")
			sub_port=$(free_port "$sub_port" "$listen_port")
			listen="$listen_host:$listen_port"
			say "Панель — порт $listen_port, подписки — порт $sub_port."
		else
			die "установка отменена: освободите порты или задайте свои (--listen 127.0.0.1:8090 --sub-port 2097)"
		fi
	fi
	# Порты подключений панель подбирает сама при их создании —
	# здесь только предупреждаем.
	note=
	port_busy tcp 443 && note="$note $(describe tcp 443)"
	port_busy udp 51820 && note="$note $(describe udp 51820)"
	if command -v docker >/dev/null 2>&1 && docker ps --format '{{.Image}}' 2>/dev/null | grep -q wg-easy; then
		note="$note wg-easy (Docker)"
	fi
	if [ -n "$note" ]; then
		say "Уже заняты:$note. Панель возьмёт для VLESS и AmneziaWG свободные"
		say "порты и подсеть (вместо 443 — 8443, 2053…; вместо 51820 — следующий)."
	fi
fi

# Пакеты: tar и curl для установки, iptables — NAT для клиентов AmneziaWG.
need=
command -v tar >/dev/null 2>&1 || need="$need tar"
[ -n "$file" ] || command -v curl >/dev/null 2>&1 || need="$need curl ca-certificates"
command -v iptables >/dev/null 2>&1 || need="$need iptables"
if [ -n "$need" ]; then
	say "Ставлю пакеты:$need"
	if command -v apt-get >/dev/null 2>&1; then
		DEBIAN_FRONTEND=noninteractive apt-get update -qq
		# shellcheck disable=SC2086
		DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $need >/dev/null
	elif command -v dnf >/dev/null 2>&1; then
		# shellcheck disable=SC2086
		dnf install -y -q $need
	elif command -v yum >/dev/null 2>&1; then
		# shellcheck disable=SC2086
		yum install -y -q $need
	else
		die "поставьте вручную:$need"
	fi
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ -z "$file" ]; then
	if [ -z "$version" ]; then
		# Последний выпуск — по адресу, куда GitHub перенаправляет /releases/latest.
		loc=$(curl -fsSIL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
			die "не удалось узнать последнюю версию (задайте --version)"
		version=${loc##*/}
		version=${version#v}
		case "$version" in
		'' | latest | releases) die "у $repo ещё нет выпусков (задайте --version или --file)" ;;
		esac
	fi
	name="eva-manager_${version}_linux_${arch}.tar.gz"
	base="https://github.com/$repo/releases/download/v$version"
	say "Скачиваю Eva Manager $version ($arch)"
	curl -fsSL --retry 3 -o "$tmp/$name" "$base/$name" || die "не удалось скачать $base/$name"
	curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$base/checksums.txt" || die "не удалось скачать checksums.txt"
	want=$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "в checksums.txt нет $name"
	got=$(sha256sum "$tmp/$name" | cut -d' ' -f1)
	[ "$got" = "$want" ] || die "контрольная сумма $name не совпала"
	file=$tmp/$name
fi
[ -f "$file" ] || die "нет файла $file"

mkdir "$tmp/x"
tar -xzf "$file" -C "$tmp/x"
src=$(find "$tmp/x" -mindepth 1 -maxdepth 1 -type d | head -n1)
[ -n "$src" ] && [ -x "$src/eva-manager" ] && [ -x "$src/xray/xray" ] ||
	die "$file — не архив выпуска Eva Manager"

# Пользователь и каталоги.
if ! id eva >/dev/null 2>&1; then
	nologin=$(command -v nologin || echo /usr/sbin/nologin)
	useradd --system --user-group --home-dir "$data" --no-create-home --shell "$nologin" eva
fi
install -d -m 0700 -o eva -g eva "$data"
install -d -m 0750 -o root -g eva "$etc"
if [ ! -f "$etc/env" ]; then
	cat >"$etc/env" <<EOF
# Настройки Eva Manager. После изменения: systemctl restart eva-manager
# Все переменные — в README (раздел «Настройки»).

# Адрес веб-интерфейса. 127.0.0.1 — только через SSH-туннель:
#   ssh -L 8080:127.0.0.1:8080 root@сервер  →  http://127.0.0.1:8080
EVA_LISTEN=$listen

# Подписки для клиентов — отдельным портом наружу, без доступа к панели.
EVA_SUB_LISTEN=0.0.0.0:$sub_port
# Внешний адрес подписок (можно задать и в Настройках панели):
#EVA_SUB_URL=https://vpn.example.com:$sub_port

# PostgreSQL вместо SQLite:
#EVA_DATABASE_URL=postgres://eva:пароль@127.0.0.1:5432/eva
EOF
	chown root:eva "$etc/env"
	chmod 0640 "$etc/env"
fi

# Программа: на месте работающей службы файлы заменяются целиком
# (install создаёт новый файл), служба перезапускается ниже.
was_active=0
systemctl is-active --quiet eva-manager 2>/dev/null && was_active=1
install -d -m 0755 "$lib" "$lib/xray"
install -m 0755 "$src/eva-manager" "$lib/eva-manager"
install -m 0755 "$src/xray/xray" "$lib/xray/xray"
for f in "$src"/xray/*.dat; do
	[ -f "$f" ] && install -m 0644 "$f" "$lib/xray/"
done
install -m 0755 "$src/eva-manager.sh" /usr/local/bin/eva-manager
install -m 0644 "$src/eva-manager.service" "$unit"

# Выход клиентов AmneziaWG в интернет: служба не root и сама включить
# ip_forward не может.
printf 'net.ipv4.ip_forward = 1\n' >/etc/sysctl.d/99-eva-manager.conf
sysctl -q -p /etc/sysctl.d/99-eva-manager.conf >/dev/null 2>&1 ||
	say "Предупреждение: не удалось включить net.ipv4.ip_forward — AmneziaWG без NAT"

if [ -c /dev/net/tun ] && [ "$(stat -c %a /dev/net/tun)" != 666 ]; then
	say "Предупреждение: /dev/net/tun доступен только root — AmneziaWG не поднимется"
	say "  (обычно права 0666: chmod 0666 /dev/net/tun)"
fi

systemctl daemon-reload
systemctl enable --quiet eva-manager
installed=$("$lib/eva-manager" version)
if [ "$start" = 0 ]; then
	say "Eva Manager $installed установлен, служба не запущена: systemctl start eva-manager"
	exit 0
fi
if [ "$was_active" = 1 ]; then
	systemctl restart eva-manager
	verb=обновлён
else
	systemctl start eva-manager
	verb=установлен
fi

# Ждём, пока панель ответит, и показываем токен первого запуска.
url=$(
	set -a
	. "$etc/env"
	set +a
	host=${EVA_LISTEN%:*}
	port=${EVA_LISTEN##*:}
	case "$host" in '' | 0.0.0.0 | '[::]') host=127.0.0.1 ;; esac
	echo "http://$host:$port"
)
ok=0
i=0
while [ $i -lt 30 ]; do
	if curl -fsS -o /dev/null "$url/api/health" 2>/dev/null; then
		ok=1
		break
	fi
	sleep 1
	i=$((i + 1))
done
if [ "$ok" = 0 ]; then
	say "Eva Manager $installed $verb, но панель не ответила на $url."
	say "Журнал: journalctl -u eva-manager -n 50"
	exit 1
fi

say ""
say "Eva Manager $installed $verb и запущен."
say "  Панель:  $url"
case "$url" in
http://127.0.0.1:* | http://localhost:*)
	say "           с вашего компьютера: ssh -L ${url##*:}:127.0.0.1:${url##*:} root@<сервер>, затем $url"
	;;
esac
if [ "$verb" = установлен ]; then
	say "  Токен для первого входа: $(/usr/local/bin/eva-manager token)"
	say "  Забыли пароль: eva-manager admin passwd"
fi
say "  Настройки: $etc/env, журнал: journalctl -u eva-manager -f"
