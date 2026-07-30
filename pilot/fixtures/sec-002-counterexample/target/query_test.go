package items

import (
	"strings"
	"testing"
)

type database struct {
	statement string
	argument  any
}

func (database *database) LookupValidated(string) error { return nil }

func (database *database) Query(statement string, arguments ...any) error {
	database.statement = statement
	database.argument = arguments[0]
	return nil
}

func TestInputRemainsBoundValue(t *testing.T) {
	db := &database{}
	attack := "x' OR 1=1 --"
	if err := Handle(db, attack); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(db.statement, attack) || db.argument != attack {
		t.Fatal("input changed SQL structure instead of remaining a bound value")
	}
}
