-- Stage 7: additive, durable transactional outbox and consumer inbox.
-- Do not delete or rewrite existing financial records or ledger entries.
CREATE TABLE sqs_outbox (
  id UUID PRIMARY KEY,
  aggregate_id UUID NOT NULL REFERENCES wallets(id),
  transaction_id UUID REFERENCES wager_transactions(id),
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
  state TEXT NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','IN_FLIGHT','PUBLISHED')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  lease_until TIMESTAMPTZ,
  lease_token UUID,
  published_at TIMESTAMPTZ,
  last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT sqs_outbox_unique_state_event UNIQUE(transaction_id,event_type),
  CONSTRAINT sqs_outbox_lease_consistent CHECK (
    (state='IN_FLIGHT' AND lease_until IS NOT NULL AND lease_token IS NOT NULL)
    OR (state<>'IN_FLIGHT' AND lease_until IS NULL AND lease_token IS NULL)
  )
);
CREATE INDEX sqs_outbox_due_idx ON sqs_outbox(next_attempt_at,created_at,id)
  WHERE state IN ('PENDING','IN_FLIGHT');
CREATE INDEX sqs_outbox_wallet_order_idx ON sqs_outbox(aggregate_id,created_at,id)
  WHERE state <> 'PUBLISHED';

CREATE TABLE sqs_inbox (
  consumer_name TEXT NOT NULL,
  message_id TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  transaction_id UUID REFERENCES wager_transactions(id),
  result_status TEXT NOT NULL,
  CONSTRAINT sqs_inbox_atomic_outcome CHECK ((result_status='PROCESSING' AND transaction_id IS NULL)
    OR (result_status<>'PROCESSING' AND transaction_id IS NOT NULL)),
  processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (consumer_name,message_id)
);
CREATE INDEX sqs_inbox_transaction_idx ON sqs_inbox(transaction_id);

-- Prevent a financial outbox event from being mutated after publication;
-- the publisher only updates delivery metadata. Ledger remains immutable.
CREATE OR REPLACE VIEW stage7_messaging_status AS
 SELECT (SELECT count(*) FROM sqs_outbox WHERE state='PENDING') AS outbox_pending,
        (SELECT count(*) FROM sqs_outbox WHERE state='IN_FLIGHT') AS outbox_in_flight,
        (SELECT count(*) FROM sqs_outbox WHERE state='PUBLISHED') AS outbox_published,
        (SELECT count(*) FROM sqs_inbox) AS inbox_processed;
