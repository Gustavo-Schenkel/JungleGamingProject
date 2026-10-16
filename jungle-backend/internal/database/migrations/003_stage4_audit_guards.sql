-- Compatible both with a fresh Stage 4 database and a Stage 4 DB with
-- previously processed test transactions. No table drops or data rewrite.
-- A ledger should not be erased by TRUNCATE either.
CREATE OR REPLACE FUNCTION prevent_ledger_truncate()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only; TRUNCATE is forbidden';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_entries_no_truncate ON ledger_entries;
CREATE TRIGGER ledger_entries_no_truncate
BEFORE TRUNCATE ON ledger_entries
FOR EACH STATEMENT EXECUTE FUNCTION prevent_ledger_truncate();

-- Reconciliation helper: LEFT JOIN includes wallets without ledger movements.
-- NUMERIC accumulation avoids bigint SUM overflow.
CREATE OR REPLACE VIEW stage4_wallet_audit AS
SELECT w.id AS wallet_id,
       w.player_id,
       w.currency,
       w.balance_minor_units,
       COALESCE(SUM(CASE l.direction
         WHEN 'CREDIT' THEN l.amount_minor_units::NUMERIC
         WHEN 'DEBIT' THEN -l.amount_minor_units::NUMERIC
         ELSE 0 END), 0) AS ledger_net_minor_units,
       COUNT(l.id) AS ledger_entry_count
FROM wallets w
LEFT JOIN ledger_entries l ON l.wallet_id=w.id
GROUP BY w.id, w.player_id, w.currency, w.balance_minor_units;
