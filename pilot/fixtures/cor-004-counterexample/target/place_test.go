package orders

import (
	"reflect"
	"strconv"
	"testing"
)

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

func TestPlacementKeepsHistoryMapsAndAddsBothRecords(t *testing.T) {
	database := &Database{
		orders: map[string]Order{"existing": {ID: "existing"}},
		outbox: map[string]Event{"existing": {ID: "existing"}},
	}
	ordersBefore := reflect.ValueOf(database.orders).Pointer()
	outboxBefore := reflect.ValueOf(database.outbox).Pointer()

	if err := Place(NewOutboxTransaction(database), Order{ID: "new"}, Event{ID: "new"}); err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(database.orders).Pointer() != ordersBefore || reflect.ValueOf(database.outbox).Pointer() != outboxBefore {
		t.Fatal("placement replaced a complete history map")
	}
	if _, ok := database.orders["new"]; !ok {
		t.Fatal("order was not committed")
	}
	if _, ok := database.outbox["new"]; !ok {
		t.Fatal("outbox event was not committed")
	}
}

func TestOutboxSnapshotsAreBoundedAndReconciliationDrainsAllBatches(t *testing.T) {
	database := &Database{orders: map[string]Order{}, outbox: map[string]Event{}}
	for index := 0; index < reconciliationBatchSize*2+1; index++ {
		id := "event-" + strconv.Itoa(index)
		database.outbox[id] = Event{ID: id}
	}
	if batch := nextOutboxBatch(database); len(batch) != reconciliationBatchSize {
		t.Fatalf("batch size = %d", len(batch))
	}

	provider := &Provider{deliveries: map[string]int{}}
	ReconcileOutbox(database, provider)
	if len(database.outbox) != 0 {
		t.Fatalf("remaining outbox events = %d", len(database.outbox))
	}
	if len(provider.deliveries) != reconciliationBatchSize*2+1 {
		t.Fatalf("deliveries = %d", len(provider.deliveries))
	}
}
