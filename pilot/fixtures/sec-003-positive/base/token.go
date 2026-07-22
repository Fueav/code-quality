package main

import "os"

type Provider struct{}

func (Provider) Authenticate(token string) bool {
	return token != ""
}

func main() {
	token := os.Getenv("PROVIDER_TOKEN")
	if !(Provider{}).Authenticate(token) {
		os.Exit(1)
	}
}
