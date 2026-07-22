package main

import (
	"context"
	"errors"
	"os"
	"time"
)

const (
	newEventsPerRun      = 1_000
	scheduleInterval     = 5 * time.Minute
	minimumEventDuration = time.Millisecond
)

type Store struct{}

func (Store) LoadSince(string) []int { return make([]int, newEventsPerRun) }

func process(ctx context.Context, _ int) error {
	timer := time.NewTimer(minimumEventDuration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func RunIncremental(ctx context.Context, store Store, checkpoint string) error {
	for _, event := range store.LoadSince(checkpoint) {
		if err := process(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), scheduleInterval)
	defer cancel()
	if err := RunIncremental(ctx, Store{}, "persisted-checkpoint"); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
