#!/bin/sh
# Архив выпуска для одной архитектуры: панель (с собранным интерфейсом),
# Xray из XRAY_VERSION, systemd-юнит и install.sh.
#
#   scripts/dist.sh <версия> <amd64|arm64> [каталог, по умолчанию bin/dist]
#
# Интерфейс должен быть уже собран (make web).
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
version=$1
arch=$2
out=${3:-$here/bin/dist}

if [ ! -f "$here/internal/web/dist/index.html" ]; then
	echo "dist: интерфейс не собран — make web" >&2
	exit 1
fi
name="eva-manager_${version}_linux_${arch}"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/$name" "$out"

(cd "$here" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
	-ldflags "-s -w -X github.com/lyovkindanil/Eva_Manager/internal/buildinfo.Version=$version" \
	-o "$stage/$name/eva-manager" ./cmd/eva-manager)
sh "$here/scripts/fetch-xray.sh" "$(cat "$here/XRAY_VERSION")" "$stage/$name/xray" "$arch" >/dev/null
cp "$here/deploy/eva-manager.service" "$here/deploy/eva-manager.sh" "$here/install.sh" "$here/LICENSE" "$stage/$name/"
tar -C "$stage" -czf "$out/$name.tar.gz" "$name"
echo "$out/$name.tar.gz"
