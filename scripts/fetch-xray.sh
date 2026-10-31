#!/bin/sh
# Скачивает Xray-core с GitHub, проверяет SHA-256 по файлу .dgst из того же
# релиза и распаковывает xray, geoip.dat и geosite.dat.
#
#   scripts/fetch-xray.sh [версия] [каталог] [архитектура]
#
# Версия по умолчанию — из файла XRAY_VERSION, каталог — ./bin/xray,
# архитектура — текущей машины (amd64 | arm64, как TARGETARCH в Docker).
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
version=${1:-$(cat "$here/XRAY_VERSION")}
dest=${2:-$here/bin/xray}
arch=${3:-$(uname -m)}

case "$arch" in
x86_64 | amd64) asset=Xray-linux-64.zip ;;
aarch64 | arm64) asset=Xray-linux-arm64-v8a.zip ;;
*)
	echo "fetch-xray: архитектура $arch не поддерживается" >&2
	exit 1
	;;
esac

url="https://github.com/XTLS/Xray-core/releases/download/v$version/$asset"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "fetch-xray: $url"
curl -fsSL --retry 3 -o "$tmp/$asset" "$url"
curl -fsSL --retry 3 -o "$tmp/$asset.dgst" "$url.dgst"

# Строка вида "SHA2-256= 0123…" (иногда "SHA2-256(файл)= 0123…").
want=$(grep -i '^SHA2-256' "$tmp/$asset.dgst" | head -n1 | sed 's/.*= *//' | tr -d ' \r')
if [ -z "$want" ]; then
	echo "fetch-xray: в $asset.dgst нет SHA2-256" >&2
	exit 1
fi
got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
if [ "$got" != "$want" ]; then
	echo "fetch-xray: SHA-256 не совпал: $got, ожидался $want" >&2
	exit 1
fi

mkdir -p "$dest"
unzip -o -q "$tmp/$asset" xray geoip.dat geosite.dat -d "$dest"
chmod 755 "$dest/xray"
"$dest/xray" version | head -n1
