-- 0001_init: initial data model (S6). Applied by goose via postgres.RunMigrations.
--
-- urls maps a short code to its target. Column notes:
--   id:         identity sequence backing shorturl.CodeGenerator
--               (NextSequence reads nextval('urls_id_seq')). BIGINT
--               GENERATED ALWAYS AS IDENTITY is the modern spelling of
--               bigserial semantics (S6) — the two cannot be combined:
--               BIGSERIAL already carries a default (SQLSTATE 42601).
--   code:       Base62 of the sequence (S3). UNIQUE is the collision
--               source of truth; the constraint name is urls_code_key
--               (Postgres default), matched by the adapter when mapping
--               23505 to shorten.ErrCodeCollision.
--   long_url:   the validated target (validation happened in the domain,
--               S7 — the database trusts what it is told).
--   created_at: DB-defaulted timestamp; the domain imports no clock (S5.1).

-- +goose Up
CREATE TABLE urls (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code       TEXT        NOT NULL UNIQUE,
    long_url   TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
-- For operators only: RunMigrations never runs downs. It exists so a
-- deployment can be rolled back deliberately with `goose down`.
DROP TABLE urls;
