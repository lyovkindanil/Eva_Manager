-- Начальная схема. Общее подмножество SQLite и PostgreSQL: идентификаторы
-- TEXT (генерирует панель), время BIGINT (секунды Unix), трафик BIGINT
-- (байты), 0 в лимите и сроке — без ограничения.

-- Узел — сервер с Xray (и позже AmneziaWG). Свой сервер панель ведёт как
-- локальный узел (is_local), остальные подключаются агентом на этапе 4.
CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT NOT NULL DEFAULT '',
    is_local BOOLEAN NOT NULL DEFAULT FALSE,
    agent_key TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL
);

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    uuid TEXT NOT NULL UNIQUE,
    sub_token TEXT NOT NULL UNIQUE,
    traffic_limit BIGINT NOT NULL DEFAULT 0,
    traffic_used BIGINT NOT NULL DEFAULT 0,
    expires_at BIGINT NOT NULL DEFAULT 0,
    -- active | disabled | expired | limited
    status TEXT NOT NULL DEFAULT 'active',
    note TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL
);

-- settings — типизированные параметры протокола в JSON (Reality, транспорт,
-- маскировка AmneziaWG); из них одна функция строит и конфиг, и ссылку.
CREATE TABLE inbounds (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL,
    port INTEGER NOT NULL,
    settings TEXT NOT NULL DEFAULT '{}',
    link_host TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at BIGINT NOT NULL
);

CREATE INDEX inbounds_node ON inbounds (node_id);

CREATE TABLE user_inbounds (
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    inbound_id TEXT NOT NULL REFERENCES inbounds (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, inbound_id)
);

-- day — 'YYYY-MM-DD' в UTC.
CREATE TABLE traffic_daily (
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    day TEXT NOT NULL,
    uplink BIGINT NOT NULL DEFAULT 0,
    downlink BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);

-- Каждый применённый конфиг Xray: из этой таблицы делается откат.
CREATE TABLE config_versions (
    node_id TEXT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    config TEXT NOT NULL,
    test_ok BOOLEAN NOT NULL,
    test_output TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    PRIMARY KEY (node_id, version)
);

CREATE TABLE admins (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    totp_secret TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
