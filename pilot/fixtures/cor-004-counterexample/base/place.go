package orders

type Order struct{ ID string }
type Event struct{ ID string }

type Transaction interface {
	SaveOrderAndEvent(Order, Event) error
}

func Place(transaction Transaction, order Order, event Event) error {
	return transaction.SaveOrderAndEvent(order, event)
}
