package teststubs

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/sharedcode/joltrin/v5"
)

// ErrorRegistryStub extends RegistryStub allowing injection of an error on UpdateNoLocks after N successful calls.
type ErrorRegistryStub struct {
	RegistryStub
	failAfter int32 // number of successful UpdateNoLocks calls before failing (-1 means never); set once at construction, read-only after
	calls     atomic.Int32
	Err       error // error to return (defaults if nil)
}

func NewErrorRegistryStub(failAfter int, err error) *ErrorRegistryStub {
	st := &ErrorRegistryStub{failAfter: int32(failAfter)}
	if err != nil {
		st.Err = err
	} else {
		st.Err = errors.New("injected UpdateNoLocks error")
	}
	return st
}

func (e *ErrorRegistryStub) UpdateNoLocks(ctx context.Context, allOrNothing bool, payloads []sop.RegistryPayload[sop.Handle]) error {
	c := e.calls.Add(1) - 1
	if e.failAfter >= 0 && c >= e.failAfter {
		return e.Err
	}
	return nil
}

// Calls returns how many UpdateNoLocks calls have been made.
func (e *ErrorRegistryStub) Calls() int { return int(e.calls.Load()) }
