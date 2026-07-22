package main

import "errors"

var ErrInvalidCredentials = errors.New("invalid credentials")

func ProductionOperation() error { return ErrInvalidCredentials }

func RunWorker(operation func() error) error {
	for {
		if err := operation(); err != nil {
			continue
		}
		return nil
	}
}

func main() {
	_ = RunWorker(ProductionOperation)
}
