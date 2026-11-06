INSERT INTO products (sku, name, price, stock, is_active) VALUES
    ('ELC-1001', 'USB-C Cable 1m',                   45000, 240, TRUE),
    ('ELC-1002', 'Wireless Mouse',                  185000,  64, TRUE),
    ('ELC-1003', 'Mechanical Keyboard',             750000,  18, TRUE),
    ('ELC-1004', '27-inch IPS Monitor',            3250000,   9, TRUE),
    ('NET-2001', 'Cat6 Patch Cable 3m',              38000, 120, TRUE),
    ('NET-2002', '8-Port Gigabit Switch',           420000,   7, TRUE),
    ('PKG-3001', 'Corrugated Box 40x30x30',          12500, 500, TRUE),
    ('PKG-3002', 'Bubble Wrap Roll 50m',            135000,  32, TRUE),
    ('PKG-3003', 'Packing Tape 48mm',                11000,   8, TRUE),
    ('OFF-4001', 'Thermal Labels 100x150 (500pcs)',  95000,  46, TRUE),
    ('OFF-4002', 'Handheld Barcode Scanner',       1150000,  15, TRUE),
    ('SAF-5001', 'Safety Gloves (pair)',             28000,  90, TRUE),
    ('SAF-5002', 'Safety Helmet',                    85000,   0, TRUE),  -- no history: can be deleted
    ('LEG-9001', 'Parallel Printer Cable',           25000,   4, FALSE),
    ('LEG-9002', 'CRT Monitor Stand',                60000,   0, FALSE); -- no history: can be deleted

INSERT INTO stock_movements (product_id, type, qty, note, created_at)
SELECT id, 'in', stock, 'Opening balance', now() - interval '14 days'
FROM products
WHERE stock > 0;

-- Performs the same writes as the API.
CREATE OR REPLACE FUNCTION pg_temp.seed_order(p_customer TEXT, p_age INTERVAL, p_lines JSONB, p_cancelled_after INTERVAL DEFAULT NULL)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE
    v_order_id INTEGER;
    v_created  TIMESTAMPTZ := now() - p_age;
    v_line     RECORD;
    v_product  products%ROWTYPE;
    v_total    BIGINT := 0;
BEGIN
    INSERT INTO sales_orders (customer, total, created_at) VALUES (p_customer, 0, v_created) RETURNING id INTO v_order_id;

    FOR v_line IN SELECT * FROM jsonb_to_recordset(p_lines) AS l(sku TEXT, qty INTEGER) LOOP
        UPDATE products SET stock = stock - v_line.qty WHERE sku = v_line.sku RETURNING * INTO STRICT v_product;
        INSERT INTO sales_order_items (order_id, sku, qty, unit_price) VALUES (v_order_id, v_line.sku, v_line.qty, v_product.price);
        INSERT INTO stock_movements (product_id, type, qty, note, created_at)
        VALUES (v_product.id, 'out', v_line.qty, 'Order #' || v_order_id, v_created);
        v_total := v_total + v_product.price::BIGINT * v_line.qty;

        IF p_cancelled_after IS NOT NULL THEN
            UPDATE products SET stock = stock + v_line.qty WHERE id = v_product.id;
            INSERT INTO stock_movements (product_id, type, qty, note, created_at)
            VALUES (v_product.id, 'in', v_line.qty, 'Order #' || v_order_id || ' cancelled', v_created + p_cancelled_after);
        END IF;
    END LOOP;

    UPDATE sales_orders
    SET total = v_total,
        status = CASE WHEN p_cancelled_after IS NULL THEN 'CREATED' ELSE 'CANCELLED' END,
        cancelled_at = v_created + p_cancelled_after
    WHERE id = v_order_id;
END $$;

SELECT pg_temp.seed_order('PT Maju Jaya',   interval '3 days',  '[{"sku":"ELC-1002","qty":4},{"sku":"PKG-3001","qty":50}]');
SELECT pg_temp.seed_order('Budi Santoso',   interval '2 days',  '[{"sku":"ELC-1003","qty":2}]', interval '4 hours');
SELECT pg_temp.seed_order('Toko Sentosa',   interval '5 hours', '[{"sku":"ELC-1004","qty":1},{"sku":"NET-2002","qty":2},{"sku":"OFF-4001","qty":3}]');
