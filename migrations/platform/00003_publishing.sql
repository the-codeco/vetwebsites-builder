-- +goose Up
-- Where each website is published (an account on somebody's web hosting) and a record of every publish.
CREATE TABLE publish_targets (
    site_id      BIGINT PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    protocol     TEXT NOT NULL CHECK (protocol IN ('sftp', 'ftps')),
    host         TEXT NOT NULL,
    port         INT NOT NULL,
    username     TEXT NOT NULL,
    password_enc BYTEA NOT NULL,           -- encrypted with the server's SECRET_KEY; never shown again
    remote_dir   TEXT NOT NULL,
    host_key     TEXT NOT NULL DEFAULT '', -- the SFTP server's fingerprint, remembered from the first connection
    tls_name     TEXT NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE publish_jobs (
    id          BIGSERIAL PRIMARY KEY,
    site_id     BIGINT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    started_by  TEXT NOT NULL DEFAULT '',
    force       BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    version     TEXT NOT NULL DEFAULT '',
    files_total INT NOT NULL DEFAULT 0,
    files_done  INT NOT NULL DEFAULT 0,
    uploaded    INT NOT NULL DEFAULT 0,
    deleted     INT NOT NULL DEFAULT 0,
    unchanged   INT NOT NULL DEFAULT 0,
    bytes       BIGINT NOT NULL DEFAULT 0,
    message     TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    warnings    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX publish_jobs_site_idx ON publish_jobs (site_id, id DESC);
-- at most one unfinished publish per website
CREATE UNIQUE INDEX publish_jobs_active_idx ON publish_jobs (site_id) WHERE status IN ('queued', 'running');

ALTER TABLE sites
    ADD COLUMN published_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN published_changes BIGINT NOT NULL DEFAULT -1,
    ADD COLUMN published_at      TIMESTAMPTZ;

-- +goose Down
ALTER TABLE sites DROP COLUMN published_version, DROP COLUMN published_changes, DROP COLUMN published_at;
DROP TABLE publish_jobs;
DROP TABLE publish_targets;
