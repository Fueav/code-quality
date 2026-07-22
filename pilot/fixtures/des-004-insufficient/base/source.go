package pricing

type Source interface {
	Price(product string) int
}

type Sources struct {
	Primary   Source
	Secondary Source
}

func FinalPrice(sources Sources, product string) int {
	return sources.Primary.Price(product)
}

func Checkout(sources Sources, product string) int {
	return FinalPrice(sources, product)
}
