CREATE TABLE IF NOT EXISTS wallets (
    id UUID PRIMARY KEY,
    player_id TEXT NOT NULL,
    currency CHAR(3) NOT NULL,
    balance_minor_units BIGINT NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT wallets_player_currency_unique UNIQUE (player_id, currency),
    CONSTRAINT wallets_player_id_not_blank CHECK (length(trim(player_id)) > 0),
    CONSTRAINT wallets_currency_uppercase CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT wallets_balance_nonnegative CHECK (balance_minor_units >= 0),
    CONSTRAINT wallets_version_positive CHECK (version >= 1)
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    transaction_id UUID NOT NULL,
    entry_type TEXT NOT NULL,
    amount_minor_units BIGINT NOT NULL,
    currency CHAR(3) NOT NULL,
    balance_after_minor_units BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ledger_wallet_transaction_unique UNIQUE (wallet_id, transaction_id),
    CONSTRAINT ledger_entry_type_valid CHECK (entry_type IN ('BET', 'WIN', 'REFUND', 'ROLLBACK', 'OPENING')),
    CONSTRAINT ledger_amount_positive CHECK (amount_minor_units > 0),
    CONSTRAINT ledger_balance_after_nonnegative CHECK (balance_after_minor_units >= 0),
    CONSTRAINT ledger_currency_uppercase CHECK (currency ~ '^[A-Z]{3}$')
);

CREATE INDEX IF NOT EXISTS ledger_wallet_created_idx ON ledger_entries (wallet_id, created_at, id);

CREATE OR REPLACE FUNCTION prevent_ledger_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only; UPDATE and DELETE are forbidden';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_entries_no_update ON ledger_entries;
CREATE TRIGGER ledger_entries_no_update BEFORE UPDATE ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION prevent_ledger_mutation();

DROP TRIGGER IF EXISTS ledger_entries_no_delete ON ledger_entries;
CREATE TRIGGER ledger_entries_no_delete BEFORE DELETE ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION prevent_ledger_mutation();
