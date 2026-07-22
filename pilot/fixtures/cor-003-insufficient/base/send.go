package transfer

type Client interface {
	Send(value int64) error
}

func TransferSettlement(client Client, amountCents int64) error {
	return client.Send(amountCents)
}
