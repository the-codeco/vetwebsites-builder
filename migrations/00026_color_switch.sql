-- +goose Up
-- Where visitors find the dark mode switch: floating at the side of the page, as a link in the footer, or nowhere.
ALTER TABLE site_settings ADD COLUMN color_switch TEXT NOT NULL DEFAULT 'floating';

DROP TRIGGER settings_changed ON site_settings;
CREATE TRIGGER settings_changed AFTER UPDATE OF title, active_theme, tokens, custom_css, seo, show_search, color_mode, color_switch, consent, footer, brand, details, tracking
    ON site_settings FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();

-- +goose Down
DROP TRIGGER settings_changed ON site_settings;
CREATE TRIGGER settings_changed AFTER UPDATE OF title, active_theme, tokens, custom_css, seo, show_search, color_mode, consent, footer, brand, details, tracking
    ON site_settings FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
ALTER TABLE site_settings DROP COLUMN color_switch;
