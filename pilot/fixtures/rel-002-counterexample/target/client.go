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

func Handle(ctx context.Context, client Client) error {
	bounded, cancel := context.WithTimeout(ctx, sharedClientTimeout)
	defer cancel()
	return client.Do(bounded)
}
