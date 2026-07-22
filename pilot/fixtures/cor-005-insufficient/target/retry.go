package notification

type Request struct{ Recipient string }

type Provider interface {
	SendPayout(Request) error
}

func Send(provider Provider, request Request) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = provider.SendPayout(request); err == nil {
			return nil
		}
	}
	return err
}
