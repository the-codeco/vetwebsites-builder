-- +goose Up
-- Messages sent through the contact form block. Only what the visitor typed is stored:
-- no IP address and no browser details.
CREATE TABLE messages (
    id         BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    name       TEXT NOT NULL,
    email      TEXT NOT NULL,
    subject    TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL,
    read       BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX messages_created_idx ON messages (created_at DESC);
CREATE INDEX messages_unread_idx ON messages (id) WHERE NOT read;

-- Where to email a copy of each new message ('' = don't email; the inbox always has it).
ALTER TABLE site_settings ADD COLUMN notify_email TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE site_settings DROP COLUMN notify_email;
DROP TABLE messages;
