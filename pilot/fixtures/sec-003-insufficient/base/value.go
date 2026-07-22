package credential

import "os"

type Provider interface {
	Authenticate(token string) bool
}

func Value() string { return os.Getenv("PROVIDER_TOKEN") }

func AuthenticateProduction(provider Provider) bool {
	return provider.Authenticate(Value())
}
