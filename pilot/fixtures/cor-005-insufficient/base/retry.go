package notification

type Request struct{ Recipient string }

type Provider interface {
	SendPayout(Request) error
}

func Send(provider Provider, request Request) error {
	return provider.SendPayout(request)
}
