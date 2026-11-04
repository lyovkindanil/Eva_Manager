# syntax=docker/dockerfile:1

# Интерфейс.
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN mkdir -p ../internal/web/dist && npm run build

# Панель: один статический бинарник со встроенным интерфейсом.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/web/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/lyovkindanil/Eva_Manager/internal/buildinfo.Version=${VERSION}" \
    -o /out/eva-manager ./cmd/eva-manager

# Xray-core: релиз с GitHub, версия из XRAY_VERSION, SHA-256 сверяется.
FROM alpine:3.22 AS xray
RUN apk add --no-cache curl unzip libcap
ARG TARGETARCH
COPY XRAY_VERSION scripts/fetch-xray.sh /tmp/
RUN sh /tmp/fetch-xray.sh "$(cat /tmp/XRAY_VERSION)" /opt/xray "${TARGETARCH:-amd64}" \
    # Xray работает не от root, но слушает 443.
    && setcap cap_net_bind_service=+ep /opt/xray/xray
# eva-awg — тот же бинарник с правами на сеть для процессов AmneziaWG
# (интерфейс, адреса, порт < 1024). Сама панель прав не получает и
# запускается и без cap_add.
COPY --from=build /out/eva-manager /opt/eva-awg
RUN setcap cap_net_admin,cap_net_bind_service=+ep /opt/eva-awg

FROM alpine:3.22
# iptables — NAT для клиентов AmneziaWG: правила ставит процесс eva-awg
# (iptables-nft или legacy — как у хоста). Права на сеть — у самих
# xtables, лок-файл и каталог сокетов принадлежат eva.
RUN apk add --no-cache ca-certificates tzdata iptables iptables-legacy \
    && apk add --no-cache --virtual .setcap libcap \
    && adduser -D -H -u 10001 eva \
    && mkdir -p /var/lib/eva-manager /run/amneziawg && chown eva:eva /var/lib/eva-manager /run/amneziawg \
    && bins=$(ls /sbin/xtables-*-multi /usr/sbin/xtables-*-multi 2>/dev/null | xargs -r -n1 readlink -f | sort -u) \
    && [ -n "$bins" ] && for b in $bins; do setcap cap_net_admin,cap_net_raw=+ep "$b"; done \
    && touch /run/xtables.lock && chown eva:eva /run/xtables.lock \
    && apk del .setcap
COPY --from=xray /opt/xray/xray /usr/local/bin/xray
COPY --from=xray /opt/xray/geoip.dat /opt/xray/geosite.dat /usr/local/share/xray/
COPY --from=build /out/eva-manager /usr/local/bin/eva-manager
COPY --from=xray /opt/eva-awg /usr/local/bin/eva-awg
ENV EVA_DATA_DIR=/var/lib/eva-manager \
    EVA_LISTEN=0.0.0.0:8080 \
    EVA_XRAY_BIN=/usr/local/bin/xray \
    XRAY_LOCATION_ASSET=/usr/local/share/xray
USER eva
VOLUME /var/lib/eva-manager
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1
ENTRYPOINT ["eva-manager"]
CMD ["serve"]
