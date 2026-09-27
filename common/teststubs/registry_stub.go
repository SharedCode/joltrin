package teststubs

import (
	"context"

	"github.com/sharedcode/joltrin/v5"
)

// RegistryStub implements sop.Registry with no-op behaviors, suitable for rollback tests.
type RegistryStub struct{}

func (s *RegistryStub) Get(ctx context.Context, payloads []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	res := make([]sop.RegistryPayload[sop.Handle], len(payloads))
	for i, p := range payloads {
		res[i].RegistryTable = p.RegistryTable
		res[i].BlobTable = p.BlobTable
		res[i].IDs = make([]sop.Handle, len(p.IDs))
		for ii, id := range p.IDs {
			res[i].IDs[ii] = sop.Handle{LogicalID: id, Version: 1}
		}
	}
	return res, nil
}
func (s *RegistryStub) Add(ctx context.Context, payloads []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (s *RegistryStub) Update(ctx context.Context, payloads []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (s *RegistryStub) UpdateNoLocks(ctx context.Context, allOrNothing bool, payloads []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (s *RegistryStub) Remove(ctx context.Context, payloads []sop.RegistryPayload[sop.UUID]) error {
	return nil
}
func (s *RegistryStub) Unlock(ctx context.Context, lk *sop.LockKey) error { return nil }
func (s *RegistryStub) Replicate(ctx context.Context, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
