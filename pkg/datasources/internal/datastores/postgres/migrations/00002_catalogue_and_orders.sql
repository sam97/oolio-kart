-- +goose Up

CREATE TABLE products (
    id          bigint PRIMARY KEY,
    name        text NOT NULL,
    price_cents bigint NOT NULL CHECK (price_cents >= 0),
    category    text NOT NULL,
    image       jsonb NOT NULL -- thumbnail, mobile, tablet and desktop URLs
);

-- The demo catalogue.
INSERT INTO products (id, name, price_cents, category, image)
SELECT id, name, price_cents, category, jsonb_build_object(
    'thumbnail', base || '-thumbnail.jpg',
    'mobile',    base || '-mobile.jpg',
    'tablet',    base || '-tablet.jpg',
    'desktop',   base || '-desktop.jpg')
FROM (VALUES
    (1, 'Waffle with Berries',       650, 'Waffle',       'waffle'),
    (2, 'Vanilla Bean Crème Brûlée', 700, 'Crème Brûlée', 'creme-brulee'),
    (3, 'Macaron Mix of Five',       800, 'Macaron',      'macaron'),
    (4, 'Classic Tiramisu',          550, 'Tiramisu',     'tiramisu'),
    (5, 'Pistachio Baklava',         400, 'Baklava',      'baklava'),
    (6, 'Lemon Meringue Pie',        500, 'Pie',          'meringue'),
    (7, 'Red Velvet Cake',           450, 'Cake',         'cake'),
    (8, 'Salted Caramel Brownie',    550, 'Brownie',      'brownie'),
    (9, 'Vanilla Panna Cotta',       650, 'Panna Cotta',  'panna-cotta')
) AS demo (id, name, price_cents, category, image),
LATERAL (SELECT 'https://orderfoodonline.deno.dev/public/images/image-' || image AS base) AS url;

CREATE TABLE orders (
    id              uuid PRIMARY KEY,
    coupon_code     text, -- null when the order had no coupon
    discounts_cents bigint NOT NULL CHECK (discounts_cents >= 0),
    total_cents     bigint NOT NULL CHECK (total_cents >= 0),
    placed_at       timestamptz NOT NULL DEFAULT now()
);

-- One row per item of the order, in the order they were given.
CREATE TABLE order_items (
    order_id         uuid NOT NULL REFERENCES orders ON DELETE CASCADE,
    line             integer NOT NULL,
    product_id       bigint NOT NULL REFERENCES products,
    quantity         integer NOT NULL CHECK (quantity BETWEEN 1 AND 100),
    unit_price_cents bigint NOT NULL, -- the price when the order was placed
    PRIMARY KEY (order_id, line)
);

-- +goose Down

DROP TABLE order_items;
DROP TABLE orders;
DROP TABLE products;
