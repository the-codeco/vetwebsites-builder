-- +goose Up
-- The page layout, header and sidebar (site_settings.layout, .sidebar) and a page's section, order and sidebar switch
-- (pages.parent_id, .position, .sidebar) change what visitors see, so they count as unpublished changes too.
DROP TRIGGER settings_changed ON site_settings;
CREATE TRIGGER settings_changed AFTER UPDATE OF title, active_theme, tokens, custom_css, seo, show_search, color_mode, color_switch, consent, footer, brand, details, tracking, layout, sidebar
    ON site_settings FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
DROP TRIGGER pages_edited ON pages;
CREATE TRIGGER pages_edited AFTER UPDATE OF slug, title, status, seo, publish_at, parent_id, position, sidebar ON pages FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();

-- +goose Down
DROP TRIGGER pages_edited ON pages;
CREATE TRIGGER pages_edited AFTER UPDATE OF slug, title, status, seo, publish_at ON pages FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
DROP TRIGGER settings_changed ON site_settings;
CREATE TRIGGER settings_changed AFTER UPDATE OF title, active_theme, tokens, custom_css, seo, show_search, color_mode, color_switch, consent, footer, brand, details, tracking
    ON site_settings FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
