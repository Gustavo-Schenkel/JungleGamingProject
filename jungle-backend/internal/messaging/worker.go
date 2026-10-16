package messaging

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

// RegisterLifecycle fails startup when SQS is enabled but queues are missing.
// All instances run independent workers; PostgreSQL leases/inbox coordinate.
func RegisterLifecycle(lc fx.Lifecycle, q *Queue, db *pgxpool.Pool, financeService *finance.Service) {
	var cancel context.CancelFunc
	var done chan struct{}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if !q.cfg.SQSEnabled {
				log.Printf("WARNING SQS_ENABLED=false; messaging disabled")
				return nil
			}
			setupCtx, stop := context.WithTimeout(ctx, 8*time.Second)
			defer stop()
			if err := q.findQueues(setupCtx); err != nil {
				return err
			}
			workerCtx, cancelFn := context.WithCancel(context.Background())
			cancel = cancelFn
			publisherDone := make(chan struct{})
			consumerDone := make(chan struct{})
			allDone := make(chan struct{})
			done = allDone
			go func() { defer close(publisherDone); q.runPublisher(workerCtx, db) }()
			go func() { defer close(consumerDone); q.runConsumer(workerCtx, db, financeService) }()
			go func() { defer close(allDone); <-publisherDone; <-consumerDone }()
			log.Printf("SQS enabled: inbound=%s outbound=%s (FIFO, durable inbox/outbox)", q.cfg.SQSIncomingQueue, q.cfg.SQSOutgoingQueue)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}

type outboxEvent struct {
	ID, WalletID, Payload, LeaseToken string
	Attempts                          int
}

// Claim one event atomically. Expired leases are safely reclaimed after crashes.
func claimOutbox(ctx context.Context, db *pgxpool.Pool) (*outboxEvent, error) {
	token := freshDedupID()
	var row outboxEvent
	err := db.QueryRow(ctx, `WITH candidate AS (
    SELECT o.id FROM sqs_outbox o
    WHERE ((o.state='PENDING' AND o.next_attempt_at<=NOW())
       OR (o.state='IN_FLIGHT' AND o.lease_until<NOW()))
       AND NOT EXISTS (SELECT 1 FROM sqs_outbox older
         WHERE older.aggregate_id=o.aggregate_id AND older.state<>'PUBLISHED'
           AND older.delivery_sequence<o.delivery_sequence)
    ORDER BY o.delivery_sequence LIMIT 1 FOR UPDATE OF o SKIP LOCKED
  ) UPDATE sqs_outbox o SET state='IN_FLIGHT',attempts=attempts+1,
    lease_token=$1::uuid,lease_until=NOW()+INTERVAL '45 seconds'
  FROM candidate WHERE o.id=candidate.id
  RETURNING o.id::text,o.aggregate_id::text,o.payload::text,o.lease_token::text,o.attempts`,
		toUUIDString(token)).Scan(&row.ID, &row.WalletID, &row.Payload, &row.LeaseToken, &row.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func toUUIDString(raw string) string {
	if len(raw) != 32 {
		return raw
	}
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

func nextBackoff(attempt int) int {
	if attempt > 8 {
		attempt = 8
	}
	if attempt < 1 {
		attempt = 1
	}
	seconds := 1 << attempt
	if seconds > 300 {
		return 300
	}
	return seconds
}

func (q *Queue) publishOne(ctx context.Context, db *pgxpool.Pool) (bool, error) {
	event, err := claimOutbox(ctx, db)
	if err != nil || event == nil {
		return false, err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	_, sendErr := q.client.SendMessage(sendCtx, &sqs.SendMessageInput{
		QueueUrl: aws.String(q.outbound), MessageBody: aws.String(event.Payload),
		MessageGroupId: aws.String(event.WalletID), MessageDeduplicationId: aws.String(event.ID),
	})
	cancel()
	// Update metadata with the claim's unique lease token: a stale publisher
	// must never overwrite a newer publisher's successful acknowledgement.
	if sendErr == nil {
		tag, err := db.Exec(ctx, `UPDATE sqs_outbox SET state='PUBLISHED',lease_until=NULL,lease_token=NULL,
     published_at=NOW(),last_error=NULL WHERE id=$1 AND lease_token=$2 AND state='IN_FLIGHT'`, event.ID, event.LeaseToken)
		if err != nil {
			return true, fmt.Errorf("ack outbox %s: %w", event.ID, err)
		}
		if tag.RowsAffected() != 1 {
			return true, fmt.Errorf("outbox lease lost %s; event may be redelivered", event.ID)
		}
		return true, nil
	}
	_, err = db.Exec(ctx, `UPDATE sqs_outbox SET state='PENDING',lease_until=NULL,lease_token=NULL,
   next_attempt_at=NOW()+($3::bigint*INTERVAL '1 second'),last_error=$4
   WHERE id=$1 AND lease_token=$2 AND state='IN_FLIGHT'`, event.ID, event.LeaseToken, int64(nextBackoff(event.Attempts)), truncateErr(sendErr))
	if err != nil {
		return true, fmt.Errorf("publisher send=%v; retry tracking=%w", sendErr, err)
	}
	return true, fmt.Errorf("SQS publish %s: %w", event.ID, sendErr)
}

func truncateErr(err error) string {
	s := err.Error()
	if len(s) > 400 {
		s = s[:400]
	}
	return s
}

func (q *Queue) runPublisher(ctx context.Context, db *pgxpool.Pool) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		for j := 0; j < 20; j++ {
			oneCtx, stop := context.WithTimeout(ctx, 17*time.Second)
			found, err := q.publishOne(oneCtx, db)
			stop()
			if err != nil && ctx.Err() == nil {
				log.Printf("SQS outbox publisher: %v", err)
			}
			if !found || ctx.Err() != nil {
				break
			}
		}
	}
}

func (q *Queue) runConsumer(ctx context.Context, db *pgxpool.Pool, service *finance.Service) {
	for ctx.Err() == nil {
		pollCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
		received, err := q.client.ReceiveMessage(pollCtx, &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(q.inbound), WaitTimeSeconds: 3, MaxNumberOfMessages: 1,
			VisibilityTimeout: 25,
		})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("SQS consumer receive: %v", err)
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}
		for _, msg := range received.Messages {
			if ctx.Err() != nil {
				return
			}
			msgCtx, stop := context.WithTimeout(ctx, 18*time.Second)
			err := q.consumeOne(msgCtx, db, service, aws.ToString(msg.MessageId), aws.ToString(msg.Body))
			if err == nil {
				_, err = q.client.DeleteMessage(msgCtx, &sqs.DeleteMessageInput{QueueUrl: aws.String(q.inbound), ReceiptHandle: msg.ReceiptHandle})
			}
			stop()
			if err != nil && ctx.Err() == nil {
				log.Printf("SQS inbox message retry/DLQ (messageId=%s): %v", aws.ToString(msg.MessageId), err)
			}
		}
	}
}

// consumeOne ensures the inbox marker and financial changes share one SQL
// commit. If a process dies before commit, neither is persisted. Deletion of
// the SQS receipt happens only AFTER this commit returns success.
func (q *Queue) consumeOne(ctx context.Context, db *pgxpool.Pool, service *finance.Service, messageID, body string) error {
	envelope, err := DecodeCommand([]byte(body))
	if err != nil {
		return fmt.Errorf("decode incoming message: %w", err)
	}
	command, key, err := VerifyCommand(q.cfg, envelope)
	if err != nil {
		return err
	}
	if messageID == "" {
		return fmt.Errorf("missing SQS message ID")
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO sqs_inbox
    (consumer_name,message_id,provider_id,idempotency_key,transaction_id,result_status)
    VALUES('jungle-financial-v1',$1,$2,$3,NULL,'PROCESSING')
    ON CONFLICT(consumer_name,message_id) DO NOTHING RETURNING message_id`,
		messageID, command.ProviderID, key).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) { // already committed: delete the redelivered SQS receipt
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("reserve inbox: %w", err)
	}
	result, err := service.ProcessInTx(ctx, tx, command, key)
	if err != nil {
		return fmt.Errorf("process SQS financial command: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE sqs_inbox SET transaction_id=$1,result_status=$2
  WHERE consumer_name='jungle-financial-v1' AND message_id=$3`, result.TransactionID, result.Status, messageID)
	if err != nil {
		return fmt.Errorf("finalize inbox: %w", err)
	}
	return tx.Commit(ctx)
}
