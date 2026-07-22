package main

import "os"

type Message struct {
	ID          string
	AmountCents int64
}

type Provider struct {
	seen      map[string]bool
	transfers int
}

func (provider *Provider) PayoutOnce(message Message) {
	if provider.seen[message.ID] {
		return
	}
	provider.seen[message.ID] = true
	provider.transfers++
}

type Queue struct{}

func (Queue) Acknowledge(string) error { return nil }

func Handle(provider *Provider, queue Queue, message Message) error {
	provider.PayoutOnce(message)
	return queue.Acknowledge(message.ID)
}

func main() {
	provider := &Provider{seen: map[string]bool{}}
	message := Message{ID: "redelivered-message", AmountCents: 10_000}
	_ = Handle(provider, Queue{}, message)
	_ = Handle(provider, Queue{}, message)
	if provider.transfers != 1 {
		os.Exit(2)
	}
}
