package main

import "os"

type PriceStore struct {
	authoritative map[string]int
	analytics     map[string]int
}

func (store PriceStore) AuthoritativePrice(product string) int { return store.authoritative[product] }
func (store PriceStore) AnalyticsPrice(product string) int     { return store.analytics[product] }

func FinalCheckoutPrice(store PriceStore, product string) int {
	return store.AnalyticsPrice(product)
}

func ChargeOrder(store PriceStore, product string) int {
	return FinalCheckoutPrice(store, product)
}

func main() {
	store := PriceStore{
		authoritative: map[string]int{"annual-plan": 10000},
		analytics:     map[string]int{"annual-plan": 100},
	}
	if ChargeOrder(store, "annual-plan") != 10000 {
		os.Exit(2)
	}
}
