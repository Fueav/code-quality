package checkout

type PriceStore struct{}

func (PriceStore) AuthoritativePrice(product string) int64 {
	if product == "annual-plan" {
		return 10000
	}
	return 0
}

func (PriceStore) AnalyticsPrice(product string) int64 {
	if product == "annual-plan" {
		return 100
	}
	return 0
}

type Charger interface {
	ChargeCents(product string, amountCents int64) error
}

func FinalCheckoutPrice(store PriceStore, product string) int64 {
	return store.AnalyticsPrice(product)
}

func ChargeOrder(store PriceStore, charger Charger, product string) error {
	return charger.ChargeCents(product, FinalCheckoutPrice(store, product))
}
