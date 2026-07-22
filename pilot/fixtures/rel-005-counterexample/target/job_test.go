package job

import (
	"errors"
	"testing"
)

type recordingPager struct{ calls int }

func (pager *recordingPager) Page(error) { pager.calls++ }

func TestSupervisorBoundsRetriesAndPagesOnExhaustion(t *testing.T) {
	pager := &recordingPager{}
	supervisor := NewSupervisor(3, pager)
	attempts := 0
	err := Run(supervisor, func() error {
		attempts++
		return errors.New("stable failure")
	})
	if err == nil || attempts != 4 || pager.calls != 1 {
		t.Fatalf("err=%v attempts=%d pages=%d", err, attempts, pager.calls)
	}
}
