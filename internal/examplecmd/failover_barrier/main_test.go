package failoverbarrier

import (
	"reflect"
	"testing"

	"github.com/sharedcode/joltrin/v5/verify"
)

func TestStopFirstIsBlockedThenRunbookCompletes(t *testing.T) {
	wf, err := newWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	blocked, committed := run(wf, []verify.StepID{"stop_primary", "verify_standby", "shift_traffic", "confirm_error_rate", "stop_primary"})
	if !reflect.DeepEqual(blocked, []verify.StepID{"stop_primary"}) {
		t.Fatalf("blocked = %v, want only the first stop_primary", blocked)
	}
	want := []verify.StepID{"verify_standby", "shift_traffic", "confirm_error_rate", "stop_primary"}
	if !reflect.DeepEqual(committed, want) {
		t.Fatalf("committed = %v, want %v", committed, want)
	}
}

func TestSkippingTheErrorRateCheckIsBlocked(t *testing.T) {
	wf, err := newWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	blocked, _ := run(wf, []verify.StepID{"verify_standby", "shift_traffic", "stop_primary"})
	if !reflect.DeepEqual(blocked, []verify.StepID{"stop_primary"}) {
		t.Fatalf("blocked = %v, want stop_primary blocked until the error rate is confirmed", blocked)
	}
}

func TestFailoverIsReachable(t *testing.T) {
	wf, err := newWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.VerifyReachability(); err != nil {
		t.Fatal(err)
	}
}
