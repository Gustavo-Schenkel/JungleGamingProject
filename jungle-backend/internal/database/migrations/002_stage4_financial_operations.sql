-- Stage 4: extend the existing Stage 3 schema in place.
-- Do not guess how to backfill ledger history: Stage 3 produced no financial entries.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM ledger_entries) THEN
    RAISE EXCEPTION 'Stage 4 migration requires reviewing existing ledger entries before adding balances and directions';
  END IF;
END;
$$;

ALTER TABLE ledger_entries
  ADD COLUMN direction TEXT NOT NULL,
  ADD COLUMN balance_before_minor_units BIGINT NOT NULL;

ALTER TABLE ledger_entries
  ADD CONSTRAINT ledger_direction_valid CHECK (direction IN ('DEBIT', 'CREDIT')),
  ADD CONSTRAINT ledger_balance_before_nonnegative CHECK (balance_before_minor_units >= 0),
  ADD CONSTRAINT ledger_consistent_balance CHECK (
    (direction = 'CREDIT' AND balance_before_minor_units::NUMERIC + amount_minor_units::NUMERIC = balance_after_minor_units::NUMERIC) OR
    (direction = 'DEBIT' AND balance_before_minor_units::NUMERIC - amount_minor_units::NUMERIC = balance_after_minor_units::NUMERIC)
  );

CREATE TABLE opening_transactions (
  id UUID PRIMARY KEY,
  wallet_id UUID NOT NULL UNIQUE REFERENCES wallets(id),
  player_id TEXT NOT NULL,
  currency CHAR(3) NOT NULL,
  amount_minor_units BIGINT NOT NULL CHECK (amount_minor_units > 0),
  status TEXT NOT NULL DEFAULT 'PROCESSED' CHECK (status = 'PROCESSED'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE wager_transactions (
  id UUID PRIMARY KEY,
  provider_id TEXT NOT NULL CHECK (length(trim(provider_id)) > 0),
  external_transaction_id TEXT NOT NULL CHECK (length(trim(external_transaction_id)) > 0),
  idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
  payload_hash CHAR(64) NOT NULL,
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  player_id TEXT NOT NULL,
  round_id TEXT NOT NULL,
  game_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('BET','WIN','LOSS','REFUND','ROLLBACK')),
  amount_minor_units BIGINT NOT NULL CHECK (amount_minor_units >= 0),
  currency CHAR(3) NOT NULL,
  reference_external_transaction_id TEXT,
  status TEXT NOT NULL CHECK (status IN ('PENDING','PENDING_REFERENCE','PROCESSED','REJECTED','FAILED')),
  failure_code TEXT,
  result_balance_minor_units BIGINT NOT NULL CHECK (result_balance_minor_units >= 0),
  result_wallet_version BIGINT NOT NULL CHECK (result_wallet_version >= 1),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT wager_provider_idempotency_unique UNIQUE(provider_id, idempotency_key),
  CONSTRAINT wager_provider_external_id_unique UNIQUE(provider_id, external_transaction_id),
  CONSTRAINT wager_stage4_status CHECK (status != 'REJECTED' OR failure_code IS NOT NULL)
);
CREATE INDEX wager_wallet_created_idx ON wager_transactions(wallet_id, created_at, id);

-- The append-only ledger triggers from 001_initial_schema.sql remain active.
