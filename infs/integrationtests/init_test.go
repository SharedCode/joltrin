//go:build integration
// +build integration

package integrationtests

import (
	"cmp"
	"context"
	"fmt"
	log "log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

// Redis config used by integration tests.
var redisConfig = redis.Options{
	Address:  "localhost:6379",
	Password: "",
	DB:       0,
}

func getDataPath() string {
	if s := os.Getenv("datapath"); s != "" {
		return s
	}
	return filepath.Join(os.TempDir(), "sop_data")
}

var dataPath = getDataPath()

var testDefaultCacheConfig sop.StoreCacheConfig

// Shared person types for tests.
type PersonKey struct {
	Firstname string
	Lastname  string
}

type Person struct {
	Gender string
	Email  string
	Phone  string
	SSN    string
}

func newPerson(fname, lname, gender, email, phone string) (PersonKey, Person) {
	return PersonKey{Firstname: fname, Lastname: lname}, Person{Gender: gender, Email: email, Phone: phone, SSN: "1234"}
}

func Compare(x PersonKey, y PersonKey) int {
	if i := cmp.Compare[string](x.Lastname, y.Lastname); i != 0 {
		return i
	}
	return cmp.Compare[string](x.Firstname, y.Firstname)
}

const (
	nodeSlotLength = 200
	batchSize      = 50
	tableName1     = "person2db_it"
	tableName2     = "twophase22"
)

// Basic EC config used by replication tests.
func initErasureCoding() {
	ec := make(map[string]sop.ErasureCodingConfig)
	ec["barstoreec"] = sop.ErasureCodingConfig{
		DataShardsCount:   2,
		ParityShardsCount: 1,
		BaseFolderPathsAcrossDrives: []string{
			fmt.Sprintf("%s%cdisk1", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk2", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk3", dataPath, os.PathSeparator),
		},
		RepairCorruptedShards: true,
	}
	ec[""] = sop.ErasureCodingConfig{
		DataShardsCount:   2,
		ParityShardsCount: 2,
		BaseFolderPathsAcrossDrives: []string{
			fmt.Sprintf("%s%cdisk4", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk5", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk6", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk7", dataPath, os.PathSeparator),
		},
		RepairCorruptedShards: true,
	}
	fs.SetGlobalErasureConfig(ec)

	// Register L2 Cache Redis.
	sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)
}

var storesFolders = []string{
	fmt.Sprintf("%s%cdisk1", dataPath, os.PathSeparator),
	fmt.Sprintf("%s%cdisk2", dataPath, os.PathSeparator),
}

var storesFoldersDefault = []string{
	fmt.Sprintf("%s%cdisk4", dataPath, os.PathSeparator),
	fmt.Sprintf("%s%cdisk5", dataPath, os.PathSeparator),
}

func TestMain(m *testing.M) {
	// Configure logging to Info.
	l := log.New(log.NewJSONHandler(os.Stdout, &log.HandlerOptions{Level: log.LevelInfo}))
	log.SetDefault(l)

	// Initialize Redis-backed components.
	conn, err := redis.OpenConnection(redisConfig)
	if err != nil {
		log.Error("Failed to open Redis connection", "error", err)
		os.Exit(1)
	}

	// Initialize erasure coding for replication tests.
	initErasureCoding()

	// Ensure base folders exist for both replication (disk1, disk2), EC defaults (disk4..disk7),
	// and an isolated set for specialized integration tests (disk8..disk13).
	for i := 1; i <= 13; i++ {
		_ = os.MkdirAll(fmt.Sprintf("%s%cdisk%d", dataPath, os.PathSeparator, i), 0o755)
	}

	// Clear Redis cache between runs to avoid cross-test contamination.
	_ = conn.Client.FlushDB(context.Background())

	// Shorten default node cache to keep tests snappy.
	testDefaultCacheConfig = sop.GetDefaultCacheConfig()
	testDefaultCacheConfig.NodeCacheDuration = time.Minute
	sop.SetDefaultCacheConfig(testDefaultCacheConfig)

	os.Exit(m.Run())
}
