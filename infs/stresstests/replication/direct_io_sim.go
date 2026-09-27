package replication

import (
	"context"
	"fmt"
	"os"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

type dioReplicationSim struct {
	fs.DirectIO
	failOnMethod int
}

func NewDirectIOReplicationSim(failOnMethod int) *dioReplicationSim {
	dio := fs.NewDirectIO()
	return &dioReplicationSim{
		failOnMethod: failOnMethod,
		DirectIO:     dio,
	}
}

func (dio dioReplicationSim) Open(ctx context.Context, filename string, flag int, permission os.FileMode) (*os.File, error) {
	if dio.failOnMethod == 1 {
		return nil, sop.Error{
			Code: sop.RestoreRegistryFileSectorFailure,
			Err:  fmt.Errorf("simulated error on Open"),
		}
	}
	return dio.DirectIO.Open(ctx, filename, flag, permission)
}
func (dio dioReplicationSim) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	if dio.failOnMethod == 2 {
		return 0, sop.Error{
			Code: sop.RestoreRegistryFileSectorFailure,
			Err:  fmt.Errorf("simulated error on WriteAt"),
		}
	}
	if dio.failOnMethod == 22 {
		return 0, sop.Error{
			Code: sop.FileIOErrorFailoverQualified,
			Err:  fmt.Errorf("simulated error on WriteAt"),
		}
	}
	return dio.DirectIO.WriteAt(ctx, file, block, offset)
}
func (dio dioReplicationSim) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	if dio.failOnMethod == 3 {
		return 0, sop.Error{
			Code: sop.RestoreRegistryFileSectorFailure,
			Err:  fmt.Errorf("simulated error on ReadAt"),
		}
	}
	return dio.DirectIO.ReadAt(ctx, file, block, offset)
}
func (dio dioReplicationSim) Close(file *os.File) error {
	if dio.failOnMethod == 4 {
		return sop.Error{
			Code: sop.RestoreRegistryFileSectorFailure,
			Err:  fmt.Errorf("simulated error on Close"),
		}
	}
	return dio.DirectIO.Close(file)
}
