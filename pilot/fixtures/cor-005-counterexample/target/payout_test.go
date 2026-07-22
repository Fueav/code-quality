package payout

import "testing"

func TestProviderIdempotencyKey(t *testing.T) {
	provider := &Provider{transfers: map[string]int{}}
	message := Message{ID: "message-1"}
	Handle(provider, message)
	Handle(provider, message)
	if provider.transfers[message.ID] != 1 {
		t.Fatal("same idempotency key transferred more than once")
	}
}
