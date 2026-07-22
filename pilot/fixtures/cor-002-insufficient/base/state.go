package settlement

type Account struct {
	AvailableCents int64
}

func Reserve(account *Account, amountCents int64) bool {
	if amountCents <= 0 || account.AvailableCents < amountCents {
		return false
	}
	account.AvailableCents -= amountCents
	return true
}

func HandleMerchantSettlement(account *Account, amountCents int64) bool {
	return Reserve(account, amountCents)
}
