package main

import (
	"errors"
	"os"
)

var (
	ErrInvalid   = errors.New("invalid request")
	ErrExhausted = errors.New("connection pool exhausted")
)

type Pool struct {
	capacity int
	inUse    int
}

type Connection struct {
	pool   *Pool
	closed bool
}

func (pool *Pool) Open() (*Connection, error) {
	if pool.inUse == pool.capacity {
		return nil, ErrExhausted
	}
	pool.inUse++
	return &Connection{pool: pool}, nil
}

func (connection *Connection) Close() {
	if !connection.closed {
		connection.closed = true
		connection.pool.inUse--
	}
}

func Handle(pool *Pool, valid bool) error {
	connection, err := pool.Open()
	if err != nil {
		return err
	}
	if !valid {
		return ErrInvalid
	}
	defer connection.Close()
	return nil
}

func main() {
	pool := &Pool{capacity: 2}
	for attempt := 0; attempt < 3; attempt++ {
		if errors.Is(Handle(pool, false), ErrExhausted) {
			os.Exit(2)
		}
	}
}
