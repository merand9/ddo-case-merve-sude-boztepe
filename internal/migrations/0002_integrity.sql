-- 0002: bütünlük tetikleyicileri

-- Eklemeli-yalnız tablolar: UPDATE / DELETE / TRUNCATE yasak.
CREATE FUNCTION forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% tablosu eklemeli-yalnızdır (append-only)', TG_TABLE_NAME
        USING ERRCODE = 'P0011';
END $$;

CREATE TRIGGER audit_log_no_update  BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_mutation();
CREATE TRIGGER product_events_no_update  BEFORE UPDATE OR DELETE ON product_events
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();
CREATE TRIGGER product_events_no_truncate BEFORE TRUNCATE ON product_events
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_mutation();

-- Ürün durum geçişi: yalnızca product_transitions'ta tanımlı geçişlere izin verilir ve
-- her geçiş product_events'e yazılır. Kimlik alanları değiştirilemez, ürün silinemez.
CREATE FUNCTION products_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ürün kayıtları silinemez' USING ERRCODE = 'P0011';
    END IF;

    IF NEW.donation_id <> OLD.donation_id OR NEW.component <> OLD.component
       OR NEW.abo <> OLD.abo OR NEW.rh_positive <> OLD.rh_positive
       OR NEW.expires_at <> OLD.expires_at OR NEW.produced_at <> OLD.produced_at
       OR NEW.parent_id IS DISTINCT FROM OLD.parent_id OR NEW.product_no <> OLD.product_no THEN
        RAISE EXCEPTION 'ürünün kimlik alanları değiştirilemez' USING ERRCODE = 'P0012';
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        IF NOT EXISTS (SELECT 1 FROM product_transitions
                       WHERE from_status = OLD.status AND to_status = NEW.status) THEN
            RAISE EXCEPTION 'geçersiz ürün durum geçişi: % -> %', OLD.status, NEW.status
                USING ERRCODE = 'P0010';
        END IF;
        INSERT INTO product_events (product_id, from_status, to_status, actor_id, reason)
        VALUES (NEW.id, OLD.status, NEW.status,
                nullif(current_setting('app.actor_id', true), '')::bigint,
                nullif(current_setting('app.reason', true), ''));
        NEW.updated_at := now();
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER products_guard_upd BEFORE UPDATE OR DELETE ON products
    FOR EACH ROW EXECUTE FUNCTION products_guard();

-- Yeni ürünün ilk durumu da olay olarak kaydedilir (toplu geçmiş yüklemesi hariç;
-- simülatör geçmiş olayları kendisi yazar).
CREATE FUNCTION products_initial_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF coalesce(current_setting('app.bulk_load', true), '') <> 'on' THEN
        INSERT INTO product_events (product_id, from_status, to_status, actor_id, reason)
        VALUES (NEW.id, NULL, NEW.status,
                nullif(current_setting('app.actor_id', true), '')::bigint,
                coalesce(nullif(current_setting('app.reason', true), ''), 'üretim'));
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER products_initial_event_ins AFTER INSERT ON products
    FOR EACH ROW EXECUTE FUNCTION products_initial_event();

-- Test sonucu yalnızca onaylanmış bir değişiklik talebiyle güncellenebilir.
CREATE FUNCTION test_results_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    change_id bigint := nullif(current_setting('app.approved_change', true), '')::bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'test sonuçları silinemez' USING ERRCODE = 'P0011';
    END IF;
    IF change_id IS NULL OR NOT EXISTS (
        SELECT 1 FROM test_change_requests
        WHERE id = change_id AND donation_id = NEW.donation_id AND status = 'approved'
          AND decided_by IS NOT NULL AND decided_by <> requested_by) THEN
        RAISE EXCEPTION 'test sonucu yalnızca çift onaylı değişiklik talebiyle güncellenebilir'
            USING ERRCODE = 'P0013';
    END IF;
    NEW.version := OLD.version + 1;
    RETURN NEW;
END $$;

CREATE TRIGGER test_results_guard_upd BEFORE UPDATE OR DELETE ON test_results
    FOR EACH ROW EXECUTE FUNCTION test_results_guard();

-- Hastaneye açık ürün görünümü: bağış / bağışçı bağlantısı içermez.
CREATE VIEW hospital_products AS
SELECT p.id, p.product_no, p.component, p.abo, p.rh_positive, p.volume_ml,
       p.expires_at, p.status
FROM products p;
