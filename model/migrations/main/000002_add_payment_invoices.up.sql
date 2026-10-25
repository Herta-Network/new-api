-- add_payment_invoices
--
-- An order is no longer bound to one payment method: each invoice asks one
-- method to collect the order, and the first invoice that is paid settles
-- it. Every gateway order made so far becomes its first invoice under its
-- own number, which its gateway knows as the order number, so a payment
-- started before the upgrade still finds it. The order keeps a copy of the
-- method, the expectation and the trade of its latest or paying invoice.

CREATE TABLE payment_invoices (
    id bigserial NOT NULL,
    invoice_no varchar(64) NOT NULL,
    order_id bigint NOT NULL,
    payment_method_id bigint NOT NULL,
    platform varchar(32) NOT NULL,
    payment_amount bigint DEFAULT 0 NOT NULL,
    payment_currency varchar(8) DEFAULT ''::character varying NOT NULL,
    exchange_rate varchar(64) DEFAULT ''::character varying NOT NULL,
    trade_no varchar(255) DEFAULT ''::character varying NOT NULL,
    status bigint NOT NULL,
    close_reason varchar(32) DEFAULT ''::character varying NOT NULL,
    created_at bigint,
    paid_at bigint DEFAULT 0 NOT NULL,
    closed_at bigint DEFAULT 0 NOT NULL,
    CONSTRAINT payment_invoices_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_payment_invoices_invoice_no ON payment_invoices (invoice_no);
CREATE INDEX idx_payment_invoices_order ON payment_invoices (order_id, id);
CREATE INDEX idx_payment_invoices_method_status ON payment_invoices (payment_method_id, status);
CREATE UNIQUE INDEX idx_payment_invoices_platform_trade ON payment_invoices (platform, trade_no) WHERE ((trade_no)::text <> ''::text);

-- A paid order's invoice is paid by the order's trade; one an administrator
-- completed by hand was not paid through its gateway and is closed. A
-- closed order's invoice is closed with it.
INSERT INTO payment_invoices (invoice_no, order_id, payment_method_id, platform, payment_amount, payment_currency,
    exchange_rate, trade_no, status, close_reason, created_at, paid_at, closed_at)
SELECT order_no, id, payment_method_id, platform, payment_amount, payment_currency, exchange_rate,
    CASE WHEN trade_no LIKE 'manual:%' THEN '' ELSE trade_no END,
    CASE
        WHEN status IN (2, 5) AND trade_no NOT LIKE 'manual:%' THEN 2
        WHEN status = 1 THEN 1
        ELSE 3
    END,
    CASE
        WHEN status IN (2, 5) AND trade_no LIKE 'manual:%' THEN 'manual'
        WHEN status = 3 THEN close_reason
        ELSE ''
    END,
    created_at,
    CASE WHEN status IN (2, 5) AND trade_no NOT LIKE 'manual:%' THEN paid_at ELSE 0 END,
    CASE
        WHEN status = 3 THEN closed_at
        WHEN status IN (2, 5) AND trade_no LIKE 'manual:%' THEN paid_at
        ELSE 0
    END
FROM payment_orders
WHERE platform <> 'balance'
ORDER BY id;

-- A method's pending checkouts are its pending invoices now.
DROP INDEX idx_payment_orders_method_status;

ALTER TABLE payment_unmatched ADD COLUMN invoice_no varchar(64) DEFAULT ''::character varying NOT NULL;
UPDATE payment_unmatched SET invoice_no = order_no WHERE platform <> 'balance' AND trade_no NOT LIKE 'manual:%';
