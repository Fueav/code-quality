package payment

type PaymentClient interface {
	ChargeCents(amountCents int64) error
}

func HandlePublicCharge(client PaymentClient, amountCents int64) error {
	return client.ChargeCents(amountCents * 100)
}
