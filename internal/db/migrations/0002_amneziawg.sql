-- AmneziaWG (этап 2). Интерфейс — сервер amneziawg-go на узле; settings —
-- JSON с ключом сервера, подсетью, MTU, DNS и маскировкой (awg.Settings).
CREATE TABLE awg_interfaces (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    iface TEXT NOT NULL,
    port INTEGER NOT NULL,
    link_host TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    settings TEXT NOT NULL DEFAULT '{}',
    created_at BIGINT NOT NULL,
    UNIQUE (node_id, iface),
    UNIQUE (node_id, name),
    UNIQUE (node_id, port)
);

-- Пир — пользователь на интерфейсе: его ключи и адрес в подсети. Ключ
-- клиента хранит панель: из него собирается конфигурация для клиента.
-- public_key — по нему счётчики UAPI сопоставляются пользователю.
CREATE TABLE awg_peers (
    interface_id TEXT NOT NULL REFERENCES awg_interfaces (id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    private_key TEXT NOT NULL,
    public_key TEXT NOT NULL,
    preshared_key TEXT NOT NULL DEFAULT '',
    address TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (interface_id, user_id),
    UNIQUE (interface_id, address),
    UNIQUE (interface_id, public_key)
);

CREATE INDEX awg_peers_user ON awg_peers (user_id);
