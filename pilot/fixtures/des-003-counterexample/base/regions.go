package regions

import "context"

type Client interface {
	BatchGet(context.Context, []string) error
	Get(context.Context, string) error
}

func SyncRegions(ctx context.Context, client Client, regions []string) error {
	return client.BatchGet(ctx, regions)
}
