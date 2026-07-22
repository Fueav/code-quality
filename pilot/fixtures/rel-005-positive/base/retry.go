package main

import (
	"errors"
	"time"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

func ProductionOperation() error { return ErrInvalidCredentials }

func RunWorker(operation func() error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = operation(); err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return err
}

func main() {
	_ = RunWorker(ProductionOperation)
}
