package main

import (
	"context"
	"errors"
	"os"
	"time"
)

const (
	changedProductionEvents = 100
	scheduleWindow          = 30 * time.Minute
	minimumProcessingTime   = time.Microsecond
)

type Store struct{}

func (Store) ChangedEvents() []int { return make([]int, changedProductionEvents) }

func processEvent(ctx context.Context, _ int) error {
	timer := time.NewTimer(minimumProcessingTime)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func RunScheduledReconciliation(ctx context.Context, store Store) error {
	for _, event := range store.ChangedEvents() {
		if err := processEvent(ctx, event); err != nil {
			return err
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
