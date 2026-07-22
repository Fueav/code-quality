package transfer

type Client interface {
	Send(value int64) error
}

func Transfer(client Client, amount int64) error {
	return client.Send(amount)
}
