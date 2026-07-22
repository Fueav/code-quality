package main

import "os"

type PaymentClient struct {
	chargedCents int64
}

func (client *PaymentClient) ChargeCents(amountCents int64) {
	client.chargedCents = amountCents
}

func HandleCharge(client *PaymentClient, amountCents int64) {
	client.ChargeCents(amountCents * 100)
}

func main() {
	client := &PaymentClient{}
	HandleCharge(client, 5_000)
	if client.chargedCents != 5_000 {
		os.Exit(2)
	}
}
