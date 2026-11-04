#!/bin/sh
# /usr/local/bin/eva-manager — обёртка install.sh: команды CLI видят те же
# настройки, что и служба (/etc/eva-manager/env), и от root работают под
# пользователем eva, чтобы не оставить в каталоге данных файлы root.
set -a
[ -r /etc/eva-manager/env ] && . /etc/eva-manager/env
set +a
bin=/usr/local/lib/eva-manager/eva-manager
if [ "$(id -u)" = 0 ]; then
	exec runuser -u eva -- "$bin" "$@"
fi
exec "$bin" "$@"
