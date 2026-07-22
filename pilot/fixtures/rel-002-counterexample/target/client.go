package downstream

import (
	"context"
	"time"
)

const (
	sharedClientTimeout = 2 * time.Second
	callerDeadline      = 5 * time.Second
)

type SharedClient struct {
	response <-chan struct{}
}

func (client SharedClient) Do(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, sharedClientTimeout)
	defer cancel()
	select {
	case <-client.response:
		return nil
	case <-bounded.Done():
		return bounded.Err()
	}
}

func Handle(_ context.Context, client SharedClient) error {
	return client.Do(context.Background())
}
