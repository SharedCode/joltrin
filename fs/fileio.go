// Package fs contains filesystem-backed implementations used by SOP.
// This file provides a thin FileIO abstraction over os with retry semantics.
package fs

import (
	"context"
	"os"
	"path/filepath"

	retry "github.com/sethvargo/go-retry"
	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/internal/pathsafety"
)

// FileIO defines filesystem operations used by this package. The default
// implementation delegates to the standard library's os package with retry
// semantics for transient errors.
type FileIO interface {
	WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error
	ReadFile(ctx context.Context, name string) ([]byte, error)
	Remove(ctx context.Context, name string) error
	Stat(ctx context.Context, path string) (os.FileInfo, error)
	Exists(ctx context.Context, path string) bool

	// Directory API.
	RemoveAll(ctx context.Context, path string) error
	MkdirAll(ctx context.Context, path string, perm os.FileMode) error
	ReadDir(ctx context.Context, sourceDir string) ([]os.DirEntry, error)
}

type defaultFileIO struct {
	errorCode sop.ErrorCode
}

// NewFileIO returns a FileIO that performs I/O via the os package with basic
// retry handling for transient errors (e.g., NFS hiccups). Directories are
// created on-demand for writes.
func NewFileIO() FileIO {
	return newFileIO(sop.FileIOErrorFailoverQualified)
}

func newFileIO(errorCode sop.ErrorCode) FileIO {
	return &defaultFileIO{
		errorCode: errorCode,
	}
}

// WriteFile writes data to a file, creating parent directories if needed, and
// retries on transient errors using SOP's retry policy.
func (dio defaultFileIO) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(name, data, perm); err != nil {
		dirPath := filepath.Dir(name)
		// Ensure parent directories exist with sensible directory permissions.
		if derr := dio.MkdirAll(ctx, dirPath, 0o755); derr == nil {
			// Parent created (or already existed): retry write on transient errors.
			return dio.retryIO(ctx, func(context.Context) error { return os.WriteFile(name, data, perm) })
		}
		// Parent creation failed: surface the original write error to the caller.
		return err
	}
	return nil
}

// ReadFile reads an entire file into memory with retry on transient errors.
func (dio defaultFileIO) ReadFile(ctx context.Context, name string) ([]byte, error) {
	var ba []byte
	err := dio.retryIO(ctx, func(context.Context) error {
		var e error
		ba, e = os.ReadFile(name)
		return e
	})
	return ba, err
}

// Remove deletes a file with retry on transient errors.
func (dio defaultFileIO) Remove(ctx context.Context, name string) error {
	return dio.retryIO(ctx, func(context.Context) error { return os.Remove(name) })
}

// Stat returns the FileInfo structure describing file.
func (dio defaultFileIO) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	var fi os.FileInfo
	err := dio.retryIO(ctx, func(context.Context) error {
		var e error
		fi, e = os.Stat(path)
		return e
	})
	return fi, err
}

// MkdirAll creates a directory tree with retry on transient errors.
func (dio defaultFileIO) MkdirAll(ctx context.Context, path string, perm os.FileMode) error {
	return dio.retryIO(ctx, func(context.Context) error { return os.MkdirAll(path, perm) })
}

// RemoveAll removes a directory tree with retry on transient errors, after
// checking the path isn't the filesystem root or a well-known system
// directory (see internal/pathsafety).
func (dio defaultFileIO) RemoveAll(ctx context.Context, path string) error {
	return dio.retryIO(ctx, func(context.Context) error { return pathsafety.RemoveAll(path) })
}

// Exists returns true if the given path exists (file or directory).
func (dio defaultFileIO) Exists(ctx context.Context, path string) bool {
	// Treat any error other than os.ErrNotExist as an "exists" signal.
	// Permission or transient I/O errors should not be interpreted as missing path.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return true
	}
	return false
}

// ReadDir reads directory entries with retry on transient errors.
func (dio defaultFileIO) ReadDir(ctx context.Context, sourceDir string) ([]os.DirEntry, error) {
	var r []os.DirEntry
	// Use SOP retry policy to soften intermittent filesystem/NFS glitches during listings.
	err := dio.retryIO(ctx, func(context.Context) error {
		var e error
		r, e = os.ReadDir(sourceDir)
		return e
	})
	return r, err
}

func (dio defaultFileIO) retryIO(ctx context.Context, task func(ctx context.Context) error) error {
	return retryIO(ctx, task, dio.errorCode)
}

// retryIO is a package-local retry helper for filesystem operations.
// It retries retryable errors per ShouldRetry and always wraps errors with sop.Error
// so upstream failover logic can classify them.
func retryIO(ctx context.Context, task func(ctx context.Context) error, errorCode sop.ErrorCode) error {
	b := retry.NewFibonacci(sop.RetryStartDuration)
	var lastErr error
	err := retry.Do(ctx, retry.WithMaxRetries(5, b), func(ctx context.Context) error {
		if err := task(ctx); err != nil {
			if sop.ShouldRetry(err) {
				if !sop.IsFailoverQualifiedIOError(err) {
					return retry.RetryableError(err)
				}
				// Wrap into sop.Error to allow failover handler to evaluate & generate failover event if
				// error is a failover qualified error.
				return retry.RetryableError(sop.Error{Code: errorCode, Err: err})
			}
			lastErr = err
			// Wrap into sop.Error to allow failover handler to evaluate & generate failover event if
			// error is a failover qualified error.
			if sop.IsFailoverQualifiedIOError(err) {
				lastErr = sop.Error{Code: errorCode, Err: err}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return lastErr
}
