-- add_payment_options
--
-- Buyers choose a payment option (Alipay, card, ...); the payment methods
-- become the channels that collect an option, each taking orders within an
-- amount range, by priority and weight. The name, icon, description, order
-- and fee buyers see move to the option. Every payment method so far becomes
-- the only channel of an option of its own, under the same id, so buyers
-- see the same choices at the same prices and every order names the option
-- it was paid with. An option without an icon gets the one buyers saw: the
-- one its platform and payment type showed.

CREATE TABLE payment_options (
    id bigserial NOT NULL,
    name varchar(100) DEFAULT ''::character varying NOT NULL,
    icon varchar(255) DEFAULT ''::character varying NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    sort bigint DEFAULT 0 NOT NULL,
    enabled boolean NOT NULL,
    fee_mode bigint DEFAULT 0 NOT NULL,
    fee_rate_bp bigint DEFAULT 0 NOT NULL,
    fee_fixed bigint DEFAULT 0 NOT NULL,
    created_at bigint,
    updated_at bigint,
    deleted_at timestamptz,
    CONSTRAINT payment_options_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_payment_options_deleted_at ON payment_options (deleted_at);

INSERT INTO payment_options (id, name, icon, description, sort, enabled, fee_mode, fee_rate_bp, fee_fixed,
    created_at, updated_at, deleted_at)
SELECT id, name,
    CASE
        WHEN icon <> '' THEN icon
        WHEN platform = 'balance' THEN 'FaWallet'
        WHEN platform = 'cryptomus' THEN 'SiBitcoin'
        WHEN platform = 'stripe' AND config LIKE '%"payment_method_type":"alipay"%' THEN 'SiAlipay'
        WHEN platform = 'stripe' AND config LIKE '%"payment_method_type":"wechat_pay"%' THEN 'RiWechatPayFill'
        WHEN platform = 'stripe' AND config LIKE '%"payment_method_type":"card"%' THEN 'FaCreditCard'
        WHEN platform = 'stripe' THEN 'SiStripe'
        WHEN platform = 'epay' AND config LIKE '%"type":"alipay"%' THEN 'SiAlipay'
        WHEN platform = 'epay' AND config LIKE '%"type":"wxpay"%' THEN 'RiWechatPayFill'
        WHEN platform = 'epay' THEN 'FaQrcode'
        ELSE ''
    END,
    description, sort, enabled, fee_mode, fee_rate_bp, fee_fixed, created_at, updated_at, deleted_at
FROM payment_methods
ORDER BY id;
SELECT setval(pg_get_serial_sequence('payment_options', 'id'), GREATEST((SELECT max(id) FROM payment_options), 1));

ALTER TABLE payment_methods
    ADD COLUMN option_id bigint DEFAULT 0 NOT NULL,
    ADD COLUMN min_amount bigint DEFAULT 0 NOT NULL,
    ADD COLUMN max_amount bigint DEFAULT 0 NOT NULL,
    ADD COLUMN priority bigint DEFAULT 0 NOT NULL,
    ADD COLUMN weight bigint DEFAULT 1 NOT NULL;
-- Each option took over its method's switch; the channel stays on, so
-- enabling the option is all it needs.
UPDATE payment_methods SET option_id = id, enabled = true;
CREATE INDEX idx_payment_methods_option_id ON payment_methods (option_id);
ALTER TABLE payment_methods
    DROP COLUMN icon,
    DROP COLUMN description,
    DROP COLUMN sort,
    DROP COLUMN fee_mode,
    DROP COLUMN fee_rate_bp,
    DROP COLUMN fee_fixed;

ALTER TABLE payment_orders ADD COLUMN payment_option_id bigint DEFAULT 0 NOT NULL;
UPDATE payment_orders SET payment_option_id = payment_method_id;
