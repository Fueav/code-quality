package downstream

import (
	"context"
	"time"
)

type Client interface {
	Do(context.Context) error
}

func Call(ctx context.Context, client Client) error {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return client.Do(bounded)
}
