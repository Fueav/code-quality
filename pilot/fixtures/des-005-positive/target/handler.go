package main

import (
	"context"
	"errors"
	"os"
	"time"
)

const (
	productionTenantRecords = 500_000
	requestDeadline         = 2 * time.Second
	minimumWriteDuration    = time.Millisecond
)

type Repository struct{}

func (Repository) TenantRecords(string) []int { return make([]int, productionTenantRecords) }

func (Repository) Rewrite(ctx context.Context, _ int) error {
	timer := time.NewTimer(minimumWriteDuration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func HandleBackfill(ctx context.Context, repository Repository, tenant string) error {
	for _, record := range repository.TenantRecords(tenant) {
		if err := repository.Rewrite(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), requestDeadline)
	defer cancel()
	if err := HandleBackfill(ctx, Repository{}, "production-tenant"); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
