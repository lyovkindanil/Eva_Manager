<p align="center"><b>Eva Manager</b> — веб-панель для серверов на Xray-core и AmneziaWG</p>

# Eva Manager

Открытая панель для своего VPN-сервера на [Xray-core](https://github.com/XTLS/Xray-core)
и [AmneziaWG](https://github.com/amnezia-vpn/amneziawg-go):
пользователи с лимитами и сроками, инбаунды без ручной правки JSON, подписки
для клиента [VPN4Eva](https://github.com/lyovkindanil/Vpn4Eva) и других
клиентов с форматом v2rayN, ключи `vpn://` для приложения AmneziaVPN.

> Статус: **этап 2 выполнен**. Есть VLESS Reality (с VLESS Encryption по
> желанию) и AmneziaWG 2.0, пользователи с трафиком, лимитами и сроками,
> подписки. Управление пока из командной строки; экраны и установщик —
> этап 3. План: [docs/plan.md](docs/plan.md).

Главное отличие от 3x-ui — надёжность: конфиг Xray и ссылки строятся из
одной модели, каждая ссылка в CI проверяется парсером VPN4Eva и настоящим
подключением, а конфиг до применения проходит `xray run -test`.

## Как устроено

```
браузер ──HTTP──▶ eva-manager (Go, один бинарник)
                    ├─ веб-интерфейс (React, встроен)
                    ├─ SQLite или PostgreSQL
                    ├─ xray (дочерний процесс) ◀──gRPC 127.0.0.1──
                    └─ eva-manager awg-run на интерфейс ◀──UAPI-сокет──
                         (amneziawg-go: awg0, NAT)
```

Подробнее: [docs/architecture.md](docs/architecture.md).

## Запуск в Docker

```sh
git clone https://github.com/lyovkindanil/Eva_Manager && cd Eva_Manager
docker compose up -d
```

Панель слушает только `127.0.0.1:8080`: до появления входа по паролю
(этап 3) открывайте её через SSH-туннель:

```sh
ssh -L 8080:127.0.0.1:8080 root@ваш-сервер
# и в браузере http://127.0.0.1:8080
```

AmneziaWG в контейнере требует `cap_add: [NET_ADMIN]` и `/dev/net/tun` —
они уже в `docker-compose.yml`. Права получает только процесс AmneziaWG
(`eva-awg`), сама панель работает без них.

С PostgreSQL вместо SQLite: раскомментируйте `EVA_DATABASE_URL` в
`docker-compose.yml` и запустите `EVA_DB_PASSWORD=… docker compose --profile postgres up -d`.

## Первые шаги

```sh
# в Docker — через docker compose exec eva-manager eva-manager …
eva-manager inbound add --name NL-1 --host vpn.example.com   # VLESS Reality
eva-manager awg add --name "NL AWG" --host vpn.example.com     # AmneziaWG, UDP 51820
eva-manager apply                                              # применить сразу (иначе за 30 с)
eva-manager user add --name danil --limit-gb 100 --days 30     # ссылки, vpn:// и подписка
eva-manager user conf --name danil > danil.conf                # файл для AmneziaVPN / AmneziaWG
```

На сервере откройте порты инбаундов (TCP) и AmneziaWG (UDP).

## Сборка

Нужны Go (версия из `go.mod`, toolchain подтянется сам) и Node.js 22.

```sh
make build   # интерфейс + bin/eva-manager
make xray    # Xray из XRAY_VERSION → bin/xray (SHA-256 сверяется)
make run     # панель с данными в bin/data
make test    # тесты
make docker  # образ
```

Тесты с PostgreSQL и настоящим Xray (так их гоняет CI):

```sh
EVA_TEST_POSTGRES_URL=postgres://eva:eva@127.0.0.1:5432/eva_test?sslmode=disable \
EVA_TEST_XRAY_BIN=$PWD/bin/xray/xray \
go test ./...
```

Тестам с настоящим интерфейсом AmneziaWG нужен root (без него они
пропускаются; CI запускает их через sudo):

```sh
sudo -E env "PATH=$PATH" go test -run 'TestRun$|TestAWGServer' ./internal/awg/ ./internal/e2e/
```

## Настройки

| Переменная | По умолчанию | Что это |
|---|---|---|
| `EVA_DATA_DIR` | `/var/lib/eva-manager` | Каталог данных: база SQLite, конфиг Xray |
| `EVA_DATABASE_URL` | `sqlite://$EVA_DATA_DIR/eva.db` | `sqlite:///путь` или `postgres://…` |
| `EVA_LISTEN` | `127.0.0.1:8080` | Адрес панели |
| `EVA_XRAY_BIN` | `xray` из `PATH` | Путь к Xray |
| `EVA_XRAY_API` | `127.0.0.1:10085` | gRPC API Xray, только loopback |
| `EVA_SUB_LISTEN` | — | Отдельный адрес только для подписок `/sub/…`, например `0.0.0.0:2096`, когда панель слушает 127.0.0.1 |
| `EVA_SUB_URL` | — | Внешний адрес подписок для ссылок, например `https://vpn.example.com:2096` |
| `EVA_AWG_SOCKET_DIR` | `/var/run/amneziawg` | UAPI-сокеты интерфейсов AmneziaWG (там их видит `awg show`) |
| `EVA_TOKEN` | из `$EVA_DATA_DIR/admin.token` | Токен администратора для `eva-manager apply` |

API панели (`/api/…`, кроме `/api/health`) требует заголовок
`Authorization: Bearer <токен>`. Токен создаётся при первом запуске в
`$EVA_DATA_DIR/admin.token` (права 0600), напечатать его —
`eva-manager token`; веб-интерфейс спрашивает его при входе. До входа по
паролю (этап 3) это единственная защита API, поэтому подписки лучше
открывать наружу через `EVA_SUB_LISTEN`, а саму панель держать на 127.0.0.1.

Команды: `eva-manager serve` (по умолчанию), `inbound`, `awg`, `user`,
`apply`, `token`, `migrate`, `xray-config`, `version` — `eva-manager help`.

## Лицензия

[GPL-3.0](LICENSE), как у клиента VPN4Eva. Xray-core — MPL-2.0.
