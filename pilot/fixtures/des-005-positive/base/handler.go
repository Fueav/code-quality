package main

import (
	"context"
	"errors"
	"os"
	"time"
)

const requestDeadline = 2 * time.Second

type Queue struct{}

func (Queue) EnqueueTenantBackfill(context.Context, string) error { return nil }

func HandleBackfill(ctx context.Context, queue Queue, tenant string) error {
	return queue.EnqueueTenantBackfill(ctx, tenant)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), requestDeadline)
	defer cancel()
	if err := HandleBackfill(ctx, Queue{}, "production-tenant"); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
