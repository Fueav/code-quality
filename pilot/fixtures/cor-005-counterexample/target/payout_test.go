package payout

import (
	"sync"
	"testing"
)

func TestProviderIdempotencyKey(t *testing.T) {
	provider := &Provider{transfers: map[string]int{}}
	message := Message{ID: "message-1"}
	Handle(provider, message)
	Handle(provider, message)
	if provider.transfers[message.ID] != 1 {
		t.Fatal("same idempotency key transferred more than once")
	}
}

func TestConcurrentRedeliveryUsesOneTransfer(t *testing.T) {
	provider := &Provider{transfers: map[string]int{}}
	message := Message{ID: "message-1"}
	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			Handle(provider, message)
		}()
	}
	group.Wait()
	if provider.transfers[message.ID] != 1 {
		t.Fatal("concurrent redelivery produced a duplicate effect")
	}
}
