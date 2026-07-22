package regions

import "context"

type Client interface {
	BatchGet(context.Context, []string) error
	Get(context.Context, string) error
}

var supportedRegions = [...]string{"us-east", "eu-west", "ap-south"}

func SyncRegions(ctx context.Context, client Client) error {
	for _, region := range supportedRegions {
		if err := client.Get(ctx, region); err != nil {
			return err
		}
	}
	return nil
}
