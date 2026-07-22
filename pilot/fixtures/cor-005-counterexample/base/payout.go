package payout

type Message struct{ ID string }

type LocalStore interface {
	Claim(messageID string) bool
}

type Provider interface {
	Payout(message Message) error
}

func Handle(store LocalStore, provider Provider, message Message) error {
	if !store.Claim(message.ID) {
		return nil
	}
	return provider.Payout(message)
}
