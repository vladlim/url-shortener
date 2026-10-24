package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type cleanerStub struct {
	called chan int
}

func (c cleanerStub) Cleanup(ctx context.Context, batchSize int) (int64, error) {
	select {
	case c.called <- batchSize:
	case <-ctx.Done():
	}

	return 0, nil
}

func TestRunCleansAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cleaner := cleanerStub{called: make(chan int)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan struct{})

	go func() {
		defer close(done)

		Run(ctx, cleaner, logger, Config{
			Interval:  time.Millisecond,
			Timeout:   time.Second,
			BatchSize: 17,
		})
	}()

	select {
	case size := <-cleaner.called:
		if size != 17 {
			t.Fatalf("expected batch size 17, got %d", size)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker never called cleanup")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}
