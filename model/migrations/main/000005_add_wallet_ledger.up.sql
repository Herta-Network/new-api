-- add_wallet_ledger
--
-- wallet_ledger records every change of a wallet balance other than the
-- calls (whose costs are the hourly quota_data), in the transaction that
-- makes it: quota is the signed change, type what made it, ref_id and
-- ref_name what it refers to (an order number, a redemption code's id and
-- name, a check-in date), operator_id the administrator of an adjustment.
-- The rows are accounting records and outlive the log retention.
--
-- wallet_balance_snapshots keeps each user's balance at the end of a day in
-- Beijing time, written after midnight only for the users whose balance
-- differs from their previous snapshot. wallet_balance_days lists the days a
-- snapshot run recorded: a day without a row has no known closing balance,
-- and a day with one has the balance of each user's latest snapshot.

CREATE TABLE wallet_ledger (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    type varchar(32) NOT NULL,
    quota bigint NOT NULL,
    ref_id varchar(64) NOT NULL DEFAULT ''::character varying,
    ref_name varchar(128) NOT NULL DEFAULT ''::character varying,
    operator_id bigint NOT NULL DEFAULT 0,
    created_at bigint NOT NULL,
    CONSTRAINT wallet_ledger_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_wallet_ledger_user_created ON wallet_ledger (user_id, created_at);

CREATE TABLE wallet_balance_snapshots (
    id bigserial NOT NULL,
    user_id bigint NOT NULL,
    day varchar(10) NOT NULL,
    quota bigint NOT NULL,
    created_at bigint NOT NULL,
    CONSTRAINT wallet_balance_snapshots_pkey PRIMARY KEY (id)
);
CREATE UNIQUE INDEX idx_wallet_balance_snapshots_user_day ON wallet_balance_snapshots (user_id, day);

CREATE TABLE wallet_balance_days (
    day varchar(10) NOT NULL,
    users bigint NOT NULL DEFAULT 0,
    created_at bigint NOT NULL,
    CONSTRAINT wallet_balance_days_pkey PRIMARY KEY (day)
);
