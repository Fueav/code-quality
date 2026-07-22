package main

import (
	"context"
	"time"
)

const requestDeadline = 5 * time.Second

type Client struct {
	response <-chan struct{}
}

func (client Client) Do(ctx context.Context) error {
	select {
	case <-client.response:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func HandleProductionRequest(_ context.Context, client Client) error {
	return client.Do(context.Background())
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), requestDeadline)
	defer cancel()
	_ = HandleProductionRequest(ctx, Client{response: make(chan struct{})})
}
