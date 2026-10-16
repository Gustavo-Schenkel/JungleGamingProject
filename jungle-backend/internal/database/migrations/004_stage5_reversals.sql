-- Stage 5 is purely additive. All Stage 4 records remain unchanged.
ALTER TABLE wager_transactions
  ADD COLUMN resolved_reference_transaction_id UUID REFERENCES wager_transactions(id),
  ADD COLUMN reference_retry_attempts INTEGER NOT NULL DEFAULT 0 CHECK (reference_retry_attempts >= 0),
  ADD COLUMN reference_next_retry_at TIMESTAMPTZ,
  ADD COLUMN reference_deadline_at TIMESTAMPTZ;

ALTER TABLE wager_transactions
  ADD CONSTRAINT stage5_valid_amount CHECK (
    (kind='LOSS' AND amount_minor_units=0) OR
    (kind IN ('BET','WIN','REFUND','ROLLBACK') AND amount_minor_units>0)
  ),
  ADD CONSTRAINT stage5_reversal_requires_reference CHECK (
    kind NOT IN ('REFUND','ROLLBACK') OR reference_external_transaction_id IS NOT NULL
  ),
  ADD CONSTRAINT stage5_pending_reference_timestamps CHECK (
    status <> 'PENDING_REFERENCE' OR
    (reference_next_retry_at IS NOT NULL AND reference_deadline_at IS NOT NULL)
  );

-- A processed reference may be reversed only once, regardless of whether the
-- reversal is REFUND or ROLLBACK. This is in addition to wallet row locking.
CREATE UNIQUE INDEX stage5_one_completed_reversal_per_reference
  ON wager_transactions(resolved_reference_transaction_id)
  WHERE status='PROCESSED' AND kind IN ('REFUND','ROLLBACK')
    AND resolved_reference_transaction_id IS NOT NULL;

CREATE INDEX stage5_pending_reference_poll_idx
  ON wager_transactions(reference_next_retry_at, id)
  WHERE status='PENDING_REFERENCE';

-- Protect final transaction outcomes against later mutation.
CREATE OR REPLACE FUNCTION stage5_prevent_terminal_rewrite() RETURNS TRIGGER AS $$
BEGIN
  IF OLD.status IN ('PROCESSED','REJECTED','FAILED') THEN
    RAISE EXCEPTION 'terminal wager transaction cannot be changed';
  END IF;
  IF OLD.status='PENDING_REFERENCE' AND NEW.status NOT IN ('PENDING_REFERENCE','PROCESSED','REJECTED') THEN
    RAISE EXCEPTION 'invalid pending reference transition';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER stage5_terminal_transactions_immutable
  BEFORE UPDATE ON wager_transactions FOR EACH ROW
  EXECUTE FUNCTION stage5_prevent_terminal_rewrite();
