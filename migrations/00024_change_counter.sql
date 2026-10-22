-- +goose Up
-- A counter that goes up whenever anything a visitor could see changes (pages, blocks, posts, menus, pictures, settings,
-- fonts, redirects, shared blocks). A platform compares it with the number it had when the site was last published to say
-- "you have changes that are not published yet". Search words, analytics, messages and subscribers do not count.
CREATE TABLE change_counter (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    n  BIGINT NOT NULL DEFAULT 0
);
INSERT INTO change_counter (id) VALUES (1);

-- the function keeps the schema it was made in, so the counter is found whichever schema a connection is looking at
-- +goose StatementBegin
CREATE FUNCTION bump_change_counter() RETURNS trigger AS $$
BEGIN
    UPDATE change_counter SET n = n + 1 WHERE id = 1;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql SET search_path FROM CURRENT;
-- +goose StatementEnd

CREATE TRIGGER pages_changed AFTER INSERT OR DELETE ON pages FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER pages_edited AFTER UPDATE OF slug, title, status, seo, publish_at ON pages FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER blocks_changed AFTER INSERT OR UPDATE OR DELETE ON blocks FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER posts_changed AFTER INSERT OR DELETE ON posts FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER posts_edited AFTER UPDATE OF slug, title, excerpt, body, cover_image, status, seo, published_at, publish_at ON posts FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER categories_changed AFTER INSERT OR UPDATE OR DELETE ON categories FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER post_categories_changed AFTER INSERT OR UPDATE OR DELETE ON post_categories FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER menus_changed AFTER INSERT OR UPDATE OR DELETE ON menus FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER media_changed AFTER INSERT OR UPDATE OR DELETE ON media FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER reusable_blocks_changed AFTER INSERT OR UPDATE OR DELETE ON reusable_blocks FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER redirects_changed AFTER INSERT OR UPDATE OR DELETE ON redirects FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER fonts_changed AFTER INSERT OR UPDATE OR DELETE ON fonts FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();
CREATE TRIGGER settings_changed AFTER UPDATE OF title, active_theme, tokens, custom_css, seo, show_search, color_mode, consent, footer, brand, details, tracking
    ON site_settings FOR EACH STATEMENT EXECUTE FUNCTION bump_change_counter();

-- +goose Down
DROP TRIGGER settings_changed ON site_settings;
DROP TRIGGER fonts_changed ON fonts;
DROP TRIGGER redirects_changed ON redirects;
DROP TRIGGER reusable_blocks_changed ON reusable_blocks;
DROP TRIGGER media_changed ON media;
DROP TRIGGER menus_changed ON menus;
DROP TRIGGER post_categories_changed ON post_categories;
DROP TRIGGER categories_changed ON categories;
DROP TRIGGER posts_edited ON posts;
DROP TRIGGER posts_changed ON posts;
DROP TRIGGER blocks_changed ON blocks;
DROP TRIGGER pages_edited ON pages;
DROP TRIGGER pages_changed ON pages;
DROP FUNCTION bump_change_counter();
DROP TABLE change_counter;
