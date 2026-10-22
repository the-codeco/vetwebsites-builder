-- +goose Up
-- First-party, cookieless analytics. One row per page view; no IP address or user agent is stored,
-- only a visitor hash that changes every day (so visitors can't be followed from one day to the next).
CREATE TABLE hits (
    id       BIGSERIAL PRIMARY KEY,
    day      DATE NOT NULL,
    path     TEXT NOT NULL,
    referrer TEXT NOT NULL DEFAULT '', -- host only, '' means direct or from this site
    visitor  TEXT NOT NULL,
    device   TEXT NOT NULL,
    browser  TEXT NOT NULL
);
CREATE INDEX hits_day_idx ON hits (day);

ALTER TABLE site_settings
    ADD COLUMN analytics_enabled BOOLEAN NOT NULL DEFAULT true,
    -- mixed into the daily visitor hash so it can't be reversed by guessing IP addresses
    ADD COLUMN analytics_secret  TEXT NOT NULL DEFAULT '';
UPDATE site_settings SET analytics_secret = gen_random_uuid()::text || gen_random_uuid()::text;

-- +goose Down
ALTER TABLE site_settings DROP COLUMN analytics_secret, DROP COLUMN analytics_enabled;
DROP TABLE hits;
