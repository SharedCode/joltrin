//go:build integration
// +build integration

package integrationtests

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

// ioFailSim is a DirectIO wrapper that can be armed to return EIO on WriteAt for
// files whose path matches a configured prefix. It maps file descriptors to paths
// on Open and checks the call count per fd to fail after N successful writes.
type ioFailSim struct {
	base fs.DirectIO

	// Configuration
	pathPrefix  atomic.Value // string
	afterCalls  atomic.Int64
	keepFailing atomic.Bool
	// When true, injected failures will use the special RestoreRegistryFileSectorFailure code
	// which forces failover even if rollback succeeds.
	useRestoreSectorFailure atomic.Bool

	// State
	fdToPath sync.Map // fd (uintptr) -> string
	fdCalls  sync.Map // fd (uintptr) -> *int64
}

func newIOFailSim() *ioFailSim {
	s := &ioFailSim{base: fs.NewDirectIO()}
	s.pathPrefix.Store("")
	s.afterCalls.Store(0)
	s.keepFailing.Store(false)
	s.useRestoreSectorFailure.Store(false)
	return s
}

// ArmWriteFail configures the simulator to fail with syscall.EIO on WriteAt
// for any file whose full path has the given prefix, after 'after' successful
// WriteAt calls. If keep is true, it will continue to fail subsequent writes; otherwise
// it fails once per fd and then resumes delegating.
func (s *ioFailSim) ArmWriteFail(pathPrefix string, after int, keep bool) {
	s.pathPrefix.Store(pathPrefix)
	s.afterCalls.Store(int64(after))
	s.keepFailing.Store(keep)
	s.useRestoreSectorFailure.Store(false)
}

// ArmWriteFailWithRestoreSector configures the simulator similar to ArmWriteFail but
// uses sop.RestoreRegistryFileSectorFailure as the error code, causing failover even
// if rollback succeeds.
func (s *ioFailSim) ArmWriteFailWithRestoreSector(pathPrefix string, after int, keep bool) {
	s.ArmWriteFail(pathPrefix, after, keep)
	s.useRestoreSectorFailure.Store(true)
}

// Reset clears configuration and state. Call between tests.
func (s *ioFailSim) Reset() {
	s.pathPrefix.Store("")
	s.afterCalls.Store(0)
	s.keepFailing.Store(false)
	s.useRestoreSectorFailure.Store(false)
	s.fdToPath = sync.Map{}
	s.fdCalls = sync.Map{}
}

// Open delegates to the base DirectIO and records the path keyed by fd.
func (s *ioFailSim) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := s.base.Open(ctx, filename, flag, perm)
	if err != nil {
		return nil, err
	}
	s.fdToPath.Store(f.Fd(), filename)
	return f, nil
}

// WriteAt will return an error when armed and the fd's path matches the prefix
// and the per-fd call count has reached the configured threshold.
func (s *ioFailSim) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	pp, _ := s.pathPrefix.Load().(string)
	if pp != "" {
		if v, ok := s.fdToPath.Load(file.Fd()); ok {
			if strings.HasPrefix(v.(string), pp) {
				// Increment counter for this fd
				var cptr *int64
				if v2, ok := s.fdCalls.Load(file.Fd()); ok {
					cptr = v2.(*int64)
				} else {
					var z int64
					cptr = &z
					s.fdCalls.Store(file.Fd(), cptr)
				}
				n := atomic.AddInt64(cptr, 1)
				if n > s.afterCalls.Load() {
					// Fail with failover-qualified error; if not keeping failures, reset counter to avoid constant trips.
					if !s.keepFailing.Load() {
						atomic.StoreInt64(cptr, -1<<60)
					}
					if s.useRestoreSectorFailure.Load() {
						return 0, sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: syscall.EIO}
					}
					return 0, sop.Error{Code: sop.FileIOErrorFailoverQualified, Err: syscall.EIO}
				}
			}
		}
	}
	return s.base.WriteAt(ctx, file, block, offset)
}

// ReadAt delegates; can be extended to simulate read failures if needed.
func (s *ioFailSim) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return s.base.ReadAt(ctx, file, block, offset)
}

// Close delegates and clears maps for the fd.
func (s *ioFailSim) Close(file *os.File) error {
	s.fdToPath.Delete(file.Fd())
	s.fdCalls.Delete(file.Fd())
	return s.base.Close(file)
}

// Global instance used by tests to arm/reset the simulator quickly.
var directIOSimInstance = newIOFailSim()

// ArmActiveRegistryWriteEIO configures fs.DirectIOSim to fail writes with EIO on the specified path prefix.
func ArmActiveRegistryWriteEIO(pathPrefix string, after int, keepFailing bool) {
	directIOSimInstance.Reset()
	directIOSimInstance.ArmWriteFail(pathPrefix, after, keepFailing)
	fs.DirectIOSim = directIOSimInstance
}

// ArmActiveRegistryRestoreSectorFail configures fs.DirectIOSim to fail writes with the
// special RestoreRegistryFileSectorFailure code on the specified path prefix.
func ArmActiveRegistryRestoreSectorFail(pathPrefix string, after int, keepFailing bool) {
	directIOSimInstance.Reset()
	directIOSimInstance.ArmWriteFailWithRestoreSector(pathPrefix, after, keepFailing)
	fs.DirectIOSim = directIOSimInstance
}

// ResetDirectIOSim disables the simulator and clears state.
func ResetDirectIOSim() {
	if s, ok := fs.DirectIOSim.(*ioFailSim); ok {
		s.Reset()
	}
	fs.DirectIOSim = nil
}
