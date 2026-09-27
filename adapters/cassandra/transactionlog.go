package cassandra

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"

	"github.com/sharedcode/joltrin/v5"
)

const (
	// DateHourLayout format mask string.
	DateHourLayout = "2006-01-02T15"

	// Transaction logging only needs the least consistency level because we only need the logs to aid in cleanup of
	// "hanged" transactions, which are rare and have no "cleanup" urgency requirement.
	//
	// Transactions are designed to auto cleanup their logs during commit or rollback.
	transactionLoggingConsistency = gocql.LocalOne
)

// Now lambda to allow unit test to inject replayable time.Now.
var Now = time.Now

// NilUUID with gocql.UUID type.
var NilUUID = gocql.UUID(sop.NilUUID)

// Returns true if id is nil or empty UUID, otherwise false.
func IsNil(id gocql.UUID) bool {
	return sop.UUID(id).IsNil()
}

type transactionLog struct {
	dummy
	connection  *Connection
	hourLockKey *sop.LockKey
	cache       sop.L2Cache
}

// NewTransactionLog returns a Cassandra-backed implementation of sop.TransactionLog.
func NewTransactionLog(customConnection *Connection, cache sop.L2Cache) sop.TransactionLog {
	return &transactionLog{
		connection:  customConnection,
		cache:       cache,
		hourLockKey: cache.CreateLockKeys([]string{"HBP"})[0],
		dummy:       dummy{},
	}
}

func (tl *transactionLog) getConnection() (*Connection, error) {
	if tl.connection != nil {
		return tl.connection, nil
	}
	return GetGlobalConnection()
}

// Add writes a log entry (commit function and payload) for the specified transaction ID into Cassandra (t_log table).
func (tl *transactionLog) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	conn, err := tl.getConnection()
	if err != nil {
		return err
	}

	insertStatement := fmt.Sprintf("INSERT INTO %s.t_log (id, c_f, c_f_p) VALUES(?,?,?);", conn.Config.Keyspace)
	qry := conn.Session.Query(insertStatement, gocql.UUID(tid), commitFunction, payload).WithContext(ctx).Consistency(transactionLoggingConsistency)
	if err := qry.Exec(); err != nil {
		return err
	}
	return nil
}

// Remove deletes transaction log records in the t_log table for the given transaction ID.
func (tl *transactionLog) Remove(ctx context.Context, tid sop.UUID) error {
	conn, err := tl.getConnection()
	if err != nil {
		return err
	}

	deleteStatement := fmt.Sprintf("DELETE FROM %s.t_log WHERE id = ?;", conn.Config.Keyspace)
	qry := conn.Session.Query(deleteStatement, gocql.UUID(tid)).WithContext(ctx).Consistency(transactionLoggingConsistency)
	if err := qry.Exec(); err != nil {
		return err
	}

	return nil
}

// NewUUID generates a new time-based UUID for use as a transaction ID.
func (tl *transactionLog) NewUUID() sop.UUID {
	return sop.UUID(gocql.UUIDFromTime(Now().UTC()))
}

// GetOne attempts to claim an old transaction-hour bucket and returns one TID and its log records for cleanup.
// If no work is available or the hour-level lock cannot be acquired, a NilUUID is returned.
func (tl *transactionLog) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	duration := time.Duration(7 * time.Hour)

	if ok, _, err := tl.cache.Lock(ctx, duration, []*sop.LockKey{tl.hourLockKey}); !ok || err != nil {
		return sop.NilUUID, "", nil, nil
	}

	hour, tid, err := tl.getOne(ctx)
	if err != nil {
		tl.cache.Unlock(ctx, []*sop.LockKey{tl.hourLockKey})
		return sop.NilUUID, hour, nil, err
	}
	if IsNil(tid) {
		// Unlock the hour.
		tl.cache.Unlock(ctx, []*sop.LockKey{tl.hourLockKey})
		return sop.NilUUID, "", nil, nil
	}

	r, err := tl.getLogsDetails(ctx, tid)
	if err != nil {
		tl.cache.Unlock(ctx, []*sop.LockKey{tl.hourLockKey})
		return sop.NilUUID, "", nil, err
	}
	// Check one more time to remove race condition issue.
	if ok, err := tl.cache.IsLocked(ctx, []*sop.LockKey{tl.hourLockKey}); !ok || err != nil {
		tl.cache.Unlock(ctx, []*sop.LockKey{tl.hourLockKey})
		// Just return nils as we can't attain a lock.
		return sop.NilUUID, "", nil, nil
	}
	return sop.UUID(tid), hour, r, nil
}

// GetOneOfHour claims work for a specific hour bucket if within the allowable window and returns one TID and its records.
func (tl *transactionLog) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	if hour == "" {
		return sop.NilUUID, nil, nil
	}
	conn, err := tl.getConnection()
	if err != nil {
		return sop.NilUUID, nil, err
	}

	t, err := time.Parse(DateHourLayout, hour)
	if err != nil {
		return sop.NilUUID, nil, err
	}

	// Put a max time of three hours for a given cleanup processor.
	mh, _ := time.Parse(DateHourLayout, Now().Format(DateHourLayout))
	if mh.Sub(t).Hours() > 4 {
		// Unlock the hour to allow open opportunity to claim the next cleanup processing.
		// Capping to 4th hour(Redis cache is set to 7hrs) maintains only one cleaner process at a time.
		tl.cache.Unlock(ctx, []*sop.LockKey{tl.hourLockKey})
		return sop.NilUUID, nil, nil
	}

	hrid := gocql.UUIDFromTime(t)

	selectStatement := fmt.Sprintf("SELECT id FROM %s.t_log WHERE id < ? LIMIT 1 ALLOW FILTERING;", conn.Config.Keyspace)
	qry := conn.Session.Query(selectStatement, hrid).WithContext(ctx).Consistency(transactionLoggingConsistency)

	iter := qry.Iter()
	var tid gocql.UUID
	for iter.Scan(&tid) {
	}
	if err := iter.Close(); err != nil {
		return sop.NilUUID, nil, err
	}

	if IsNil(tid) {
		// Unlock the hour.
		tl.cache.Unlock(ctx, []*sop.LockKey{tl.hourLockKey})
		return sop.NilUUID, nil, nil
	}

	r, err := tl.getLogsDetails(ctx, tid)
	return sop.UUID(tid), r, err
}

// getOne returns the hour string and a candidate transaction ID older than the capped window for cleanup.
func (tl *transactionLog) getOne(ctx context.Context) (string, gocql.UUID, error) {
	mh, _ := time.Parse(DateHourLayout, Now().Format(DateHourLayout))
	// 70 minute capped hour as transaction has a max of 60min "commit time". 10 min
	// gap ensures no issue due to overlapping.
	cappedHour := mh.Add(-time.Duration(70 * time.Minute))
	cappedHourTID := gocql.UUIDFromTime(cappedHour)

	conn, err := tl.getConnection()
	if err != nil {
		return "", NilUUID, err
	}

	selectStatement := fmt.Sprintf("SELECT id FROM %s.t_log WHERE id < ? LIMIT 1 ALLOW FILTERING;", conn.Config.Keyspace)
	qry := conn.Session.Query(selectStatement, cappedHourTID).WithContext(ctx).Consistency(transactionLoggingConsistency)

	iter := qry.Iter()
	var tid gocql.UUID
	for iter.Scan(&tid) {
	}
	if err := iter.Close(); err != nil {
		return "", NilUUID, err
	}
	return cappedHour.Format(DateHourLayout), tid, nil
}

// getLogsDetails reads all commit records for the specified transaction ID from Cassandra.
func (tl *transactionLog) getLogsDetails(ctx context.Context, tid gocql.UUID) ([]sop.KeyValuePair[int, []byte], error) {
	conn, err := tl.getConnection()
	if err != nil {
		return nil, err
	}

	selectStatement := fmt.Sprintf("SELECT c_f, c_f_p FROM %s.t_log WHERE id = ?;", conn.Config.Keyspace)
	qry := conn.Session.Query(selectStatement, tid).WithContext(ctx).Consistency(transactionLoggingConsistency)

	iter := qry.Iter()
	r := make([]sop.KeyValuePair[int, []byte], 0, iter.NumRows())
	var c_f int
	var c_f_p []byte
	for iter.Scan(&c_f, &c_f_p) {
		r = append(r, sop.KeyValuePair[int, []byte]{
			Key:   c_f,
			Value: c_f_p,
		})
	}
	if err := iter.Close(); err != nil {
		return r, err
	}
	return r, nil
}

// PriorityLog returns a no-op priority log implementation for Cassandra (priority logs are FS-specific).
func (tl *transactionLog) PriorityLog() sop.TransactionPriorityLog {
	return tl.dummy
}

type dummy struct{}

func (d dummy) IsEnabled() bool {
	return false
}

// Fetch the transaction priority logs details given a tranasction ID.
func (d dummy) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	// Nothing to do here because this is only applicable/in use in File System based transaction logger.
	return nil, nil
}

// GetBatch will fetch the oldest transaction (older than 2 min) priority logs details if there are from the
// File System active home folder.
func (d dummy) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}

func (d dummy) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}

// Log commit changes to its own log file separate than the rest of transaction logs.
// This is a special log file only used during "reinstate" of drives back for replication.
func (d dummy) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, newRootNodesHandles, addedNodesHandles, updatedNodesHandles, removedNodesHandles []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

func (d dummy) Add(ctx context.Context, tid sop.UUID, payload []byte) error {
	return nil
}

// Remove will delete transaction log(t_log) records given a transaction ID(tid).
func (d dummy) Remove(ctx context.Context, tid sop.UUID) error {
	return nil
}

// Write a backup file for the priority log contents (payload).
// Backup APIs removed; no-op methods deleted.
