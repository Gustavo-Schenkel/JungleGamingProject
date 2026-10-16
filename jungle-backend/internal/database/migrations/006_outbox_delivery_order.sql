-- NOW() is transaction-start time and can precede a competing wallet lock.
-- New events receive their order at INSERT, while the wallet lock is held.
-- Existing rows retain their historical (created_at,id) order during backfill.
ALTER TABLE sqs_outbox ADD COLUMN delivery_sequence BIGINT;
WITH ordered AS (
  SELECT id, row_number() OVER (ORDER BY created_at,id) AS position FROM sqs_outbox
)
UPDATE sqs_outbox o SET delivery_sequence=ordered.position FROM ordered WHERE o.id=ordered.id;
CREATE SEQUENCE sqs_outbox_delivery_sequence_seq OWNED BY sqs_outbox.delivery_sequence;
SELECT setval('sqs_outbox_delivery_sequence_seq',
  COALESCE((SELECT max(delivery_sequence) FROM sqs_outbox),0)+1,false);
ALTER TABLE sqs_outbox ALTER COLUMN delivery_sequence SET DEFAULT nextval('sqs_outbox_delivery_sequence_seq');
ALTER TABLE sqs_outbox ALTER COLUMN delivery_sequence SET NOT NULL;
CREATE UNIQUE INDEX sqs_outbox_delivery_sequence_unique ON sqs_outbox(delivery_sequence);
CREATE INDEX sqs_outbox_wallet_delivery_idx ON sqs_outbox(aggregate_id,delivery_sequence)
  WHERE state <> 'PUBLISHED';
