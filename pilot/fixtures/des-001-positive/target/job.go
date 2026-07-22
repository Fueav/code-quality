package main

import (
	"context"
	"errors"
	"os"
	"time"
)

const (
	productionAccounts    = 200_000
	productionEvents      = 800_000
	scheduleWindow        = 30 * time.Minute
	minimumComparisonTime = time.Microsecond
)

type Store struct{}

func (Store) AllAccounts() []int { return make([]int, productionAccounts) }
func (Store) AllEvents() []int   { return make([]int, productionEvents) }

func compare(ctx context.Context, _, _ int) error {
	timer := time.NewTimer(minimumComparisonTime)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func RunScheduledReconciliation(ctx context.Context, store Store) error {
	for _, account := range store.AllAccounts() {
		for _, event := range store.AllEvents() {
			if err := compare(ctx, account, event); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), scheduleWindow)
	defer cancel()
	if err := RunScheduledReconciliation(ctx, Store{}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
