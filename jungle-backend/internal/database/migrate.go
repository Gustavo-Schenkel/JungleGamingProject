package database

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// The advisory lock serializes migrations across independent application processes.
// A dedicated pooled connection holds it until all pending migrations finish.
const migrationLockID int64 = 4400142026

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()

	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// Unlock with a fresh context, even when migration context was cancelled.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	if _, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version TEXT PRIMARY KEY,
        applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    )`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var applied bool
		if err = conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied {
			continue
		}

		content, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err = tx.Exec(ctx, string(content)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("execute migration %s: %w", name, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return validateSchema(ctx, conn)
}

// Fail with a precise diagnostic if a different installation or legacy schema
// is accidentally configured. CREATE TABLE IF NOT EXISTS cannot repair a table.
func validateSchema(ctx context.Context, conn *pgxpool.Conn) error {
	for table, columns := range map[string][]string{
		"wallets":            {"id", "player_id", "currency", "balance_minor_units", "version"},
		"ledger_entries":     {"wallet_id", "transaction_id", "amount_minor_units", "balance_before_minor_units", "balance_after_minor_units", "direction"},
		"wager_transactions": {"provider_id", "external_transaction_id", "idempotency_key", "payload_hash", "status"},
		"sqs_outbox":         {"id", "aggregate_id", "payload", "state", "lease_token", "delivery_sequence"},
		"sqs_inbox":          {"consumer_name", "message_id", "transaction_id", "result_status"},
	} {
		for _, column := range columns {
			var exists bool
			err := conn.QueryRow(ctx, `SELECT EXISTS (
                SELECT 1 FROM information_schema.columns
                WHERE table_schema='public' AND table_name=$1 AND column_name=$2
            )`, table, column).Scan(&exists)
			if err != nil {
				return fmt.Errorf("inspect schema %s.%s: %w", table, column, err)
			}
			if !exists {
				return fmt.Errorf("incompatible database schema: missing public.%s.%s; confirm POSTGRES_PORT=5433 and migration history", table, column)
			}
		}
	}
	return nil
}
