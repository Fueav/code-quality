package orders

import "testing"

func TestAtomicOutboxAndIdempotentDelivery(t *testing.T) {
	database := &Database{orders: map[string]Order{}, outbox: map[string]Event{}}
	event := Event{ID: "event-1"}
	if err := Place(NewOutboxTransaction(database), Order{ID: "order-1"}, event); err != nil {
		t.Fatal(err)
	}
	if len(database.orders) != 1 || len(database.outbox) != 1 {
		t.Fatal("order and outbox were not committed together")
	}
	provider := &Provider{deliveries: map[string]int{}}
	ReconcileOutbox(database, provider)
	if len(database.outbox) != 0 {
		t.Fatal("reconciler did not remove the delivered event")
	}
	database.outbox[event.ID] = event // Simulate retry after delivery but before acknowledgement.
	ReconcileOutbox(database, provider)
	if provider.deliveries[event.ID] != 1 {
		t.Fatal("redelivery produced a duplicate effect")
	}
}
