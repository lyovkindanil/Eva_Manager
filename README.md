<p align="center"><b>Eva Manager</b> — веб-панель для серверов на Xray-core</p>

# Eva Manager

Открытая панель для своего VPN-сервера на [Xray-core](https://github.com/XTLS/Xray-core):
пользователи с лимитами и сроками, инбаунды без ручной правки JSON, подписки
для клиента [VPN4Eva](https://github.com/lyovkindanil/Vpn4Eva) и других
клиентов с форматом v2rayN.

> Статус: **этап 0, каркас**. Панель запускает Xray, проверяет конфиг,
> следит за ядром и показывает его состояние. Пользователей и инбаундов
> пока нет: это этапы 1–3. План: [docs/plan.md](docs/plan.md).

Главное отличие от 3x-ui — надёжность: конфиг Xray и ссылки строятся из
одной модели, каждая ссылка в CI проверяется парсером VPN4Eva и настоящим
подключением, а конфиг до применения проходит `xray run -test`.

## Как устроено

```
браузер ──HTTP──▶ eva-manager (Go, один бинарник)
                    ├─ веб-интерфейс (React, встроен)
                    ├─ SQLite или PostgreSQL
                    └─ xray (дочерний процесс) ◀──gRPC 127.0.0.1──
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

С PostgreSQL вместо SQLite: раскомментируйте `EVA_DATABASE_URL` в
`docker-compose.yml` и запустите `EVA_DB_PASSWORD=… docker compose --profile postgres up -d`.

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

## Настройки

| Переменная | По умолчанию | Что это |
|---|---|---|
| `EVA_DATA_DIR` | `/var/lib/eva-manager` | Каталог данных: база SQLite, конфиг Xray |
| `EVA_DATABASE_URL` | `sqlite://$EVA_DATA_DIR/eva.db` | `sqlite:///путь` или `postgres://…` |
| `EVA_LISTEN` | `127.0.0.1:8080` | Адрес панели |
| `EVA_XRAY_BIN` | `xray` из `PATH` | Путь к Xray |
| `EVA_XRAY_API` | `127.0.0.1:10085` | gRPC API Xray, только loopback |

Команды: `eva-manager serve` (по умолчанию), `migrate`, `xray-config`, `version`.

## Лицензия

[GPL-3.0](LICENSE), как у клиента VPN4Eva. Xray-core — MPL-2.0.
