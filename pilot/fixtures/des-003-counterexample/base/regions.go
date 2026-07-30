package regions

import (
	"context"
	"errors"
)

type Client interface {
	BatchGet(context.Context, []string) error
	Get(context.Context, string) error
}

const maxSupportedRegions = 3

func SyncRegions(ctx context.Context, client Client, regions []string) error {
	if len(regions) > maxSupportedRegions {
		return errors.New("too many regions")
	}
	return client.BatchGet(ctx, regions)
}
