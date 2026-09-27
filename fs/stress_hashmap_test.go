//go:build stress
// +build stress

package fs

import (
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

const (
	hashModValue = MinimumModValue
)

// This hashing algorithm tend to be denser as more data segment file is used. At two, it can fill around 66% avg.
// At one segment file, it fills up around 55%. SOP b-tree (w/ load distribution)
// can fill up around 55%-75%, so, this is at par. BUT better for its use-case, i.e. each Handle is a very small sized data (record).
// At 4, it should be able to fill ~75%.

func TestHashModDistribution(t *testing.T) {
	hashTable := make([][handlesPerBlock]sop.UUID, hashModValue)
	collisionCount := 0
	fmt.Printf("Start %v", time.Now())
	for i := 0; i < 12000; i++ {
		// Split UUID into high & low int64 parts.
		id := sop.NewUUID()
		bytes := id[:]

		var high uint64
		for i := 0; i < 8; i++ {
			high = high<<8 | uint64(bytes[i])
		}
		var low uint64
		for i := 8; i < 16; i++ {
			low = low<<8 | uint64(bytes[i])
		}

		bucket := high % hashModValue
		bucketOffset := low % uint64(len(hashTable[0]))

		if hashTable[bucket][bucketOffset] != sop.NilUUID {
			foundASlot := false
			for ii := 0; ii < len(hashTable[0]); ii++ {
				if hashTable[bucket][ii] == sop.NilUUID {
					hashTable[bucket][ii] = id
					foundASlot = true
					break
				}
			}
			if !foundASlot {
				collisionCount++
			}
			continue
		}
		hashTable[bucket][bucketOffset] = id
	}

	notUsedSlot := 0
	notFoundCount := 0

	for i := 0; i < len(hashTable); i++ {
		for ii := 0; ii < len(hashTable[0]); ii++ {
			if hashTable[i][ii] == sop.NilUUID {
				notUsedSlot++
				continue
			}
			if !findItem(hashTable, hashTable[i][ii]) {
				notFoundCount++
			}
		}
	}
	fmt.Printf("not found count: %v, collision count: %v, not used slot: %v\n", notFoundCount, collisionCount, notUsedSlot)
	fmt.Printf("End %v", time.Now())
}

func findItem(ht [][handlesPerBlock]sop.UUID, id sop.UUID) bool {
	bytes := id[:]
	var high uint64
	for i := 0; i < 8; i++ {
		high = high<<8 | uint64(bytes[i])
	}
	var low uint64
	for i := 8; i < 16; i++ {
		low = low<<8 | uint64(bytes[i])
	}

	bucket := high % hashModValue
	bucketOffset := low % uint64(len(ht[0]))

	if ht[bucket][bucketOffset] == id {
		return true
	}
	for i := 0; i < len(ht[0]); i++ {
		if ht[bucket][i] == id {
			return true
		}
	}
	return false
}
