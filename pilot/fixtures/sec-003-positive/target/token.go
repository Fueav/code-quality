package main

import (
	"log"
	"os"
)

const acceptedProductionToken = "prod_live_fixture_7d3"

type Provider struct{}

func (Provider) Authenticate(token string) bool {
	return token == acceptedProductionToken
}

func main() {
	token := "prod_live_fixture_7d3"
	if !(Provider{}).Authenticate(token) {
		os.Exit(1)
	}
	log.Printf("token=%s", token)
}
