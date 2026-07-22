package downstream

import "context"

type Client interface {
	Do(context.Context) error
}

func Handle(ctx context.Context, client Client) error {
	return client.Do(ctx)
}
