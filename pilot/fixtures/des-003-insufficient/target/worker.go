package worker

import "context"

type Client interface {
	BatchGet(context.Context, []string) error
	Get(context.Context, string) error
}

func Sync(ctx context.Context, client Client, ids []string) error {
	for _, id := range ids {
		if err := client.Get(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
