package main

import (
	"errors"
	"os"
)

var ErrWorkerLost = errors.New("worker lost before acknowledgement")

type Message struct {
	ID          string
	AmountCents int64
}

type Provider struct{ transfers int }

func (provider *Provider) Payout(Message) {
	provider.transfers++
}

type Queue struct{}

func (Queue) Acknowledge(string) error { return ErrWorkerLost }

func Handle(provider *Provider, queue Queue, message Message) error {
	provider.Payout(message)
	return queue.Acknowledge(message.ID)
}

func main() {
	provider := &Provider{}
	message := Message{ID: "redelivered-message", AmountCents: 10_000}
	_ = Handle(provider, Queue{}, message)
	_ = Handle(provider, Queue{}, message)
	if provider.transfers != 1 {
		os.Exit(2)
	}
}
