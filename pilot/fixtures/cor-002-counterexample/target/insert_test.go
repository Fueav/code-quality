package records

import (
	"errors"
	"testing"
)

type conflictingStore struct{}

func (conflictingStore) Exists(string) bool  { panic("preflight check must not run") }
func (conflictingStore) Insert(string) error { return ErrDuplicate }

func TestUniqueConflictIsDocumentedNoOp(t *testing.T) {
	if err := InsertUnique(conflictingStore{}, "same-value"); err != nil {
		t.Fatalf("InsertUnique returned %v, want nil", err)
	}
}

type failedStore struct{ err error }

func (failedStore) Exists(string) bool        { return false }
func (store failedStore) Insert(string) error { return store.err }

func TestNonConflictErrorIsPreserved(t *testing.T) {
	want := errors.New("database unavailable")
	if got := InsertUnique(failedStore{err: want}, "value"); !errors.Is(got, want) {
		t.Fatalf("InsertUnique returned %v, want %v", got, want)
	}
}
