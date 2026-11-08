-- add_wallet_ledger_note
--
-- wallet_ledger.note is what the operator wrote about a change: the reason
-- an administrator gives for an adjustment, which the user reads on their
-- statement. Changes without one keep an empty note.

ALTER TABLE wallet_ledger ADD COLUMN note varchar(200) NOT NULL DEFAULT ''::character varying;
