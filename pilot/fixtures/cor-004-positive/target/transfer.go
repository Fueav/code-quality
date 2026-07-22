package main

import (
	"errors"
	"os"
)

var ErrLedgerUnavailable = errors.New("ledger unavailable")

type Account struct{ BalanceCents int64 }

func (account *Account) CommitDebit(amountCents int64) {
	account.BalanceCents -= amountCents
}

type Ledger struct{ Unavailable bool }

func (ledger *Ledger) WriteDebit(int64) error {
	if ledger.Unavailable {
		return ErrLedgerUnavailable
	}
	return nil
}

func Transfer(account *Account, ledger *Ledger, amountCents int64) error {
	account.CommitDebit(amountCents)
	return ledger.WriteDebit(amountCents)
}

func main() {
	account := &Account{BalanceCents: 10_000}
	err := Transfer(account, &Ledger{Unavailable: true}, 5_000)
	if !errors.Is(err, ErrLedgerUnavailable) || account.BalanceCents != 10_000 {
		os.Exit(2)
	}
}
