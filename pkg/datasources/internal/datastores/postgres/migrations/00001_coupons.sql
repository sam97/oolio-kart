-- +goose Up

-- The rules for valid coupons and the discount they give. One row; changing
-- the length or file rules makes the next coupons-job run rebuild.
CREATE TABLE coupon_settings (
    id               boolean PRIMARY KEY DEFAULT true CHECK (id),
    min_length       integer NOT NULL CHECK (min_length >= 1),
    max_length       integer NOT NULL CHECK (max_length BETWEEN min_length AND 10),
    min_files        integer NOT NULL CHECK (min_files >= 1),
    discount_percent integer NOT NULL CHECK (discount_percent BETWEEN 0 AND 100),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

INSERT INTO coupon_settings (min_length, max_length, min_files, discount_percent)
VALUES (8, 10, 2, 10);

-- The published valid codes, replaced as a whole by each build.
CREATE TABLE coupon_codes (
    code text PRIMARY KEY
);

-- What the published codes were built from, and the bucket files the build
-- left on disk. One row, written with the codes; none until the first build.
CREATE TABLE coupon_manifest (
    id       boolean PRIMARY KEY DEFAULT true CHECK (id),
    rules    text NOT NULL,
    sources  jsonb NOT NULL,
    layout   jsonb NOT NULL,
    stats    jsonb NOT NULL,
    built_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE coupon_manifest;
DROP TABLE coupon_codes;
DROP TABLE coupon_settings;
