package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

func NewPool(cfg config.Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL config: %w", err)
	}
	pc.MaxConns = 10
	pc.MinConns = 1
	pc.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	return pool, nil
}

func RegisterLifecycle(lc fx.Lifecycle, pool *pgxpool.Pool) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			startupCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			if err := pool.Ping(startupCtx); err != nil {
				return fmt.Errorf("connect PostgreSQL: %w", err)
			}
			conn := pool.Config().ConnConfig
			log.Printf("PostgreSQL connected: host=%s port=%d database=%s user=%s", conn.Host, conn.Port, conn.Database, conn.User)
			if err := runMigrations(startupCtx, pool); err != nil {
				return fmt.Errorf("run migrations: %w", err)
			}
			return nil
		},
		OnStop: func(context.Context) error { pool.Close(); return nil },
	})
}
