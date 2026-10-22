-- +goose Up
-- Sent mail: newsletters to the confirmed subscribers, and replies to contact messages.

-- A subscriber's own unsubscribe link needs the token itself (the confirm token is stored only as a hash), so
-- one is kept for each address that has been sent a newsletter. It can only unsubscribe that one address.
ALTER TABLE subscribers ADD COLUMN unsub_token TEXT UNIQUE;

CREATE TABLE campaigns (
    id          BIGSERIAL PRIMARY KEY,
    subject     TEXT NOT NULL,
    body        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sending', 'sent', 'canceled')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  BIGINT REFERENCES users (id) ON DELETE SET NULL,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    total       INT NOT NULL DEFAULT 0,        -- how many people it was sent to, fixed when sending starts
    base_url    TEXT NOT NULL DEFAULT '',      -- the site address the unsubscribe links are made from
    site_title  TEXT NOT NULL DEFAULT ''
);

-- One row per person per newsletter: a queue the sender works through, one at a time.
CREATE TABLE deliveries (
    campaign_id   BIGINT NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    subscriber_id BIGINT NOT NULL REFERENCES subscribers (id) ON DELETE CASCADE, -- unsubscribing removes the row
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts      INT NOT NULL DEFAULT 0,
    next_attempt  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error    TEXT NOT NULL DEFAULT '',
    sent_at       TIMESTAMPTZ,
    PRIMARY KEY (campaign_id, subscriber_id)
);
CREATE INDEX deliveries_due_idx ON deliveries (next_attempt) WHERE status = 'pending';

CREATE TABLE message_replies (
    id         BIGSERIAL PRIMARY KEY,
    message_id BIGINT NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    user_id    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX message_replies_message_idx ON message_replies (message_id, id);
CREATE INDEX message_replies_user_idx ON message_replies (user_id, created_at);

-- +goose Down
DROP TABLE message_replies;
DROP TABLE deliveries;
DROP TABLE campaigns;
ALTER TABLE subscribers DROP COLUMN unsub_token;
