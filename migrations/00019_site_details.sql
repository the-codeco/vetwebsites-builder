-- +goose Up
-- brand: the logo and browser-tab icon. details: phone, email, address, opening hours and social links.
-- tracking: optional analytics services (Google Analytics, Plausible). Empty means none.
ALTER TABLE site_settings
    ADD COLUMN brand    JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN details  JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN tracking JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE site_settings DROP COLUMN brand, DROP COLUMN details, DROP COLUMN tracking;
