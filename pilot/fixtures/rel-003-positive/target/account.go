package main

import (
	"os"
	"sync"
)

type Account struct{ balance int64 }

func (account *Account) Add(delta int64) {
	account.balance += delta
}

type Server struct{ account *Account }

func (server Server) HandleConfirmedUpdate(delta int64, group *sync.WaitGroup) {
	defer group.Done()
	server.account.Add(delta)
}

func main() {
	account := &Account{balance: 100}
	server := Server{account: account}
	var group sync.WaitGroup
	group.Add(2)
	go server.HandleConfirmedUpdate(10, &group)
	go server.HandleConfirmedUpdate(10, &group)
	group.Wait()
	if account.balance != 120 {
		os.Exit(2)
	}
}
