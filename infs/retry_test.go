package infs

import (
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func Test_ShouldRetry(t *testing.T) {
	err := os.ErrNotExist
	if sop.ShouldRetry(err) {
		t.FailNow()
	}
	err = os.ErrPermission
	if sop.ShouldRetry(err) {
		t.FailNow()
	}
	err = os.ErrClosed
	if sop.ShouldRetry(err) {
		t.FailNow()
	}
	err = fmt.Errorf("aa foo read-only file system bar")
	if sop.ShouldRetry(err) {
		t.FailNow()
	}
	err = nil
	if sop.ShouldRetry(err) {
		t.FailNow()
	}
	err = fmt.Errorf("file IO error, should retry")
	if !sop.ShouldRetry(err) {
		t.FailNow()
	}
}
