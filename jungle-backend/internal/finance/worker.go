package finance

import (
	"context"
	"go.uber.org/fx"
	"log"
	"time"
)

// RegisterReferenceWorker resumes unresolved external references after restarts.
// Every iteration is safe with multiple application processes; PostgreSQL
// coordinates updates and holds the durable retry state.
func RegisterReferenceWorker(lc fx.Lifecycle, service *Service) {
	var cancel context.CancelFunc
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			go func() {
				defer close(done)
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						sweepCtx, stop := context.WithTimeout(ctx, 5*time.Second)
						err := service.RetryDueReferences(sweepCtx)
						stop()
						if err != nil && ctx.Err() == nil {
							log.Printf("pending reference worker: %v", err)
						}
					}
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
