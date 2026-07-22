package worker

import "context"

type Client interface {
	BatchGet(context.Context, []string) error
	Get(context.Context, string) error
}

func Sync(ctx context.Context, client Client, ids []string) error {
	return client.BatchGet(ctx, ids)
}
