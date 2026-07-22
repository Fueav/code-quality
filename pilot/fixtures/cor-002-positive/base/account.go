package main

import "os"

type Account struct{ BalanceCents int64 }

func Debit(account *Account, amountCents int64) bool {
	if amountCents > account.BalanceCents {
		return false
	}
	account.BalanceCents -= amountCents
	return true
}

func ProcessDebit(account *Account, amountCents int64) bool {
	return Debit(account, amountCents)
}

func main() {
	account := &Account{BalanceCents: 50000}
	if ProcessDebit(account, 70000) || account.BalanceCents != 50000 {
		os.Exit(2)
	}
}
