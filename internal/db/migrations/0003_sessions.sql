-- Вход в веб-интерфейс (этап 3). В базе — только SHA-256 токена сессии:
-- утёкшая база не даёт войти чужими сессиями.
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL REFERENCES admins (id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL
);

CREATE INDEX sessions_admin ON sessions (admin_id);
