package main

import (
	"errors"
	"os"
)

var ErrLedgerUnavailable = errors.New("ledger unavailable")

type Account struct{ BalanceCents int64 }

type Ledger struct{ Unavailable bool }

func (ledger *Ledger) WriteDebit(int64) error {
	if ledger.Unavailable {
		return ErrLedgerUnavailable
	}
	return nil
}

func Transfer(account *Account, ledger *Ledger, amountCents int64) error {
	stagedBalance := account.BalanceCents - amountCents
	if err := ledger.WriteDebit(amountCents); err != nil {
		return err
	}
	account.BalanceCents = stagedBalance
	return nil
}

func main() {
	account := &Account{BalanceCents: 10_000}
	err := Transfer(account, &Ledger{Unavailable: true}, 5_000)
	if !errors.Is(err, ErrLedgerUnavailable) || account.BalanceCents != 10_000 {
		os.Exit(2)
	}
}
