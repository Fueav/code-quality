package credential

import "log"

type Provider interface {
	Authenticate(token string) bool
}

func Value() string { return "sk_example_redacted_7d3" }

func AuthenticateProduction(provider Provider) bool {
	token := Value()
	log.Printf("provider_token=%s", token)
	return provider.Authenticate(token)
}
