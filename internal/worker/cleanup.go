package worker

import (
	"context"
	"log/slog"
	"time"
)

type Cleaner interface {
	Cleanup(context.Context, int) (int64, error)
}

type Config struct {
	Interval  time.Duration
	Timeout   time.Duration
	BatchSize int
}

// Run blocks until cancellation. Each tick deletes at most one batch, bounding DB load.
func Run(ctx context.Context, service Cleaner, logger *slog.Logger, cfg Config) {
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			batchCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
			count, err := service.Cleanup(batchCtx, cfg.BatchSize)
			cancel()
			if err != nil && ctx.Err() == nil {
				logger.Error("cleanup failed", "error", err)
			}

			if count > 0 {
				logger.Info("expired links deleted", "count", count)
			}
		}
	}
}
