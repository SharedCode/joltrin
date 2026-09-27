package fs

import (
	"context"
	"os"

	"github.com/ncw/directio"
	"github.com/sharedcode/joltrin/v5"
)

// DirectIO exposes unbuffered file operations using O_DIRECT semantics where
// supported. It is intended for large, block-aligned I/O on segment files.
// Implementations should be used with directio.AlignedBlock buffers and block-aligned offsets.
type DirectIO interface {
	// Open opens a file with the given name and flags using direct I/O when possible.
	Open(ctx context.Context, filename string, flag int, permission os.FileMode) (*os.File, error)
	// WriteAt writes a block at the given offset.
	WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error)
	// ReadAt reads a block at the given offset.
	ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error)
	// Close closes the provided file handle.
	Close(file *os.File) error
}

const (
	// blockSize is the alignment size required by the direct I/O implementation.
	blockSize = directio.BlockSize
)

type directIO struct{}

// NewDirectIO returns a DirectIO implementation backed by github.com/ncw/directio.
func NewDirectIO() DirectIO {
	return &directIO{}
}

// Open wraps directio.OpenFile with SOP retry semantics. Transient errors are wrapped
// as retryable to allow the caller's policy to reattempt.
func (dio directIO) Open(ctx context.Context, filename string, flag int, permission os.FileMode) (*os.File, error) {
	var f *os.File
	err := retryIO(ctx, func(context.Context) error {
		var e error
		f, e = directio.OpenFile(filename, flag, permission)
		return e
	}, sop.FileIOErrorFailoverQualified)
	return f, err
}

// WriteAt writes a block at an aligned offset, retrying transient errors via SOP's retry helper.
// The caller is responsible for providing an aligned buffer (e.g., via directio.AlignedBlock).
func (dio directIO) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	var n int
	err := retryIO(ctx, func(ctx context.Context) error {
		var e error
		n, e = file.WriteAt(block, offset)
		return e
	}, sop.FileIOErrorFailoverQualified)
	return n, err
}

// ReadAt reads a block at an aligned offset, retrying transient errors via SOP's retry helper.
// The caller is responsible for providing an aligned buffer (e.g., via directio.AlignedBlock).
func (dio directIO) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	var n int
	err := retryIO(ctx, func(ctx context.Context) error {
		var e error
		n, e = file.ReadAt(block, offset)
		return e
	}, sop.FileIOErrorFailoverQualified)
	return n, err
}

func (dio directIO) Close(file *os.File) error {
	return file.Close()
}
