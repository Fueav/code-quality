package credential

type Provider interface {
	Authenticate(token string) bool
}

func Value() string { return "acct_token_7d3f29a18c4b" }

func AuthenticateProduction(provider Provider) bool {
	return provider.Authenticate(Value())
}
