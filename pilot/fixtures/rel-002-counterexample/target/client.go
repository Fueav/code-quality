package downstream

import (
	"context"
	"time"
)

const (
	sharedClientTimeout = 2 * time.Second
	callerDeadline      = 5 * time.Second
)

type Client interface {
	Do(context.Context) error
}

func Handle(_ context.Context, client Client) error {
	bounded, cancel := context.WithTimeout(context.Background(), sharedClientTimeout)
	defer cancel()
	return client.Do(bounded)
}
