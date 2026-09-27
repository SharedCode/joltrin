package mocks

import (
	"context"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

type MockTransactionLog struct {
	datesLogs inmemory.BtreeInterface[string, map[sop.UUID][]sop.KeyValuePair[int, []byte]]
	logsDates map[sop.UUID]string
}

const DateHourLayout = "2006-01-02T15"

func NewMockTransactionLog() sop.TransactionLog {
	return &MockTransactionLog{
		datesLogs: inmemory.NewBtree[string, map[sop.UUID][]sop.KeyValuePair[int, []byte]](true),
		logsDates: make(map[sop.UUID]string),
	}
}

// GetOne returns the oldest transaction ID.
func (tl *MockTransactionLog) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	if tl.datesLogs.First() {
		kt, _ := time.Parse(DateHourLayout, tl.datesLogs.GetCurrentKey())
		// Cap the returned entries to older than an hour to safeguard ongoing transactions.
		nt, _ := time.Parse(DateHourLayout, time.Now().Format(DateHourLayout))
		cappedTime := nt.Add(-time.Duration(1 * time.Hour))
		if kt.Unix() < cappedTime.Unix() {
			v := tl.datesLogs.GetCurrentValue()
			for kk, vv := range v {
				r := make([]sop.KeyValuePair[int, []byte], len(vv))
				for ii := range vv {
					r[ii].Key = vv[ii].Key
					r[ii].Value = vv[ii].Value
				}
				return kk, tl.datesLogs.GetCurrentKey(), r, nil
			}
		}
	}
	return sop.NilUUID, "", nil, nil
}

func (tl *MockTransactionLog) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	if !tl.datesLogs.Find(hour, false) {
		return sop.NilUUID, nil, nil
	}
	v := tl.datesLogs.GetCurrentValue()
	for kk, vv := range v {
		r := make([]sop.KeyValuePair[int, []byte], len(vv))
		for ii := range vv {
			r[ii].Key = vv[ii].Key
			r[ii].Value = vv[ii].Value
		}
		return kk, r, nil
	}
	return sop.NilUUID, nil, nil
}

func (tl *MockTransactionLog) GetTIDLogs(tid sop.UUID) []sop.KeyValuePair[int, []byte] {
	if tl.datesLogs.First() {
		for {
			v := tl.datesLogs.GetCurrentValue()
			for kk, vv := range v {
				if kk != tid {
					continue
				}
				r := make([]sop.KeyValuePair[int, []byte], len(vv))
				for ii := range vv {
					r[ii].Key = vv[ii].Key
					r[ii].Value = vv[ii].Value
				}
				return r
			}
			if !tl.datesLogs.Next() {
				break
			}
		}
	}
	return nil
}

// Add blob(s) to the Blob store.
func (tl *MockTransactionLog) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	date := time.Now().Format(DateHourLayout)
	found := tl.datesLogs.Find(date, false)
	dayLogs := tl.datesLogs.GetCurrentValue()
	if dayLogs == nil {
		dayLogs = make(map[sop.UUID][]sop.KeyValuePair[int, []byte])
	}
	dayLogs[tid] = append(dayLogs[tid], sop.KeyValuePair[int, []byte]{
		Key:   commitFunction,
		Value: payload,
	})
	if found {
		tl.datesLogs.UpdateCurrentValue(dayLogs)
	} else {
		tl.datesLogs.Add(date, dayLogs)
	}
	tl.logsDates[tid] = date
	return nil
}

// Remove will delete(non-logged) node records from different Blob stores(node tables).
func (tl *MockTransactionLog) Remove(ctx context.Context, tid sop.UUID) error {
	date := tl.logsDates[tid]
	if tl.datesLogs.Find(date, false) {
		for {
			dayLogs := tl.datesLogs.GetCurrentValue()
			if _, ok := dayLogs[tid]; ok {
				delete(dayLogs, tid)
				if len(dayLogs) == 0 {
					tl.datesLogs.RemoveCurrentItem()
				} else {
					tl.datesLogs.UpdateCurrentValue(dayLogs)
				}
				return nil
			}
			if !tl.datesLogs.Next() {
				break
			}
		}
	}
	return nil
}

// Generates a new UUID based on time.
func (tl *MockTransactionLog) NewUUID() sop.UUID {
	return sop.NewUUID()
}

func (tl *MockTransactionLog) PriorityLog() sop.TransactionPriorityLog {
	return dummyPriorityLog{}
}

// Provide a no-op priority log for tests to avoid nil dereferences in onIdle/commit paths.
type dummyPriorityLog struct{}

func (d dummyPriorityLog) IsEnabled() bool                                             { return false }
func (d dummyPriorityLog) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (d dummyPriorityLog) Remove(ctx context.Context, tid sop.UUID) error              { return nil }
func (d dummyPriorityLog) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (d dummyPriorityLog) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}

func (d dummyPriorityLog) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (d dummyPriorityLog) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, newRootNodesHandles, addedNodesHandles, updatedNodesHandles, removedNodesHandles []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (d dummyPriorityLog) GetAll(ctx context.Context) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}

// Fetch the transaction logs details given a tranasction ID.
func (tl *MockTransactionLog) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	// Nothing to do here because this is only applicable/in use in File System based transaction logger.
	return nil, nil
}

// GetLog will fetch the oldest transaction (older than 2 min) priority logs details if there are from the
// File System active home folder.
func (tl *MockTransactionLog) GetLog(ctx context.Context) (sop.UUID, []sop.RegistryPayload[sop.Handle], error) {
	return sop.NilUUID, nil, nil
}
