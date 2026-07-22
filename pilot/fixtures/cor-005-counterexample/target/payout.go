package payout

import "sync"

type Message struct{ ID string }

type Request struct {
	IdempotencyKey string
}

type Provider struct {
	mu        sync.Mutex
	transfers map[string]int
}

func (provider *Provider) Payout(request Request) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.transfers[request.IdempotencyKey] != 0 {
		return
	}
	provider.transfers[request.IdempotencyKey] = 1
}

func Handle(provider *Provider, message Message) {
	provider.Payout(Request{IdempotencyKey: message.ID})
}
