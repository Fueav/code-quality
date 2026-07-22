package main

import (
	"context"
	"errors"
	"os"
	"time"
)

const (
	activeProductionUsers = 600_000
	syncLease             = 5 * time.Minute
)

type Client struct{}

func (Client) BatchGet(context.Context, []string) error { return nil }

// Get cannot complete sooner than 10ms and honors lease cancellation.
func (Client) Get(ctx context.Context, _ string) error {
	select {
	case <-time.After(10 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), syncLease)
	defer cancel()
	if err := RunScheduledSync(ctx, Client{}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func RunScheduledSync(ctx context.Context, client Client) error {
	userIDs := make([]string, activeProductionUsers)
	return SyncUsers(ctx, client, userIDs)
}

func SyncUsers(ctx context.Context, client Client, userIDs []string) error {
	return client.BatchGet(ctx, userIDs)
}
