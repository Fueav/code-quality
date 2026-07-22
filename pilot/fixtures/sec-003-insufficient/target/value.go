package credential

import "log"

type Provider interface {
	Authenticate(token string) bool
}

func Value() string { return "acct_token_7d3f29a18c4b" }

func AuthenticateProduction(provider Provider) bool {
	token := Value()
	log.Printf("provider_token=%s", token)
	return provider.Authenticate(token)
}
