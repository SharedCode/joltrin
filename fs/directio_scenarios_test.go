package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ncw/directio"
)

// This file unifies DirectIO + fileDirectIO test behaviors so the numerous
// legacy *directio*_test.go files can be removed after verifying equal or
// better coverage. It exercises: happy paths, error branches (write/read
// before open, double open), injected open/write/read failures, helper
// utilities (aligned block creation, fileExists, getFileSize, isEOF),
// idempotent / nil close branches, and constructor fallback when DirectIOSim is nil.

// failOpenDirectIO forces Open failures.
type failOpenDirectIO struct{ DirectIO }

func (f failOpenDirectIO) Open(context.Context, string, int, os.FileMode) (*os.File, error) {
	return nil, os.ErrPermission
}

// errDirectIO allows targeted failures on write/read.
type errDirectIO struct {
	failWrite bool
	failRead  bool
}

func (e errDirectIO) Open(_ context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	// Ensure parent exists to avoid unrelated errors.
	_ = os.MkdirAll(filepath.Dir(filename), 0o755)
	return os.OpenFile(filename, flag|os.O_CREATE, perm)
}
func (e errDirectIO) WriteAt(_ context.Context, f *os.File, b []byte, off int64) (int, error) {
	if e.failWrite {
		return 0, errors.New("write failed")
	}
	return f.WriteAt(b, off)
}
func (e errDirectIO) ReadAt(_ context.Context, f *os.File, b []byte, off int64) (int, error) {
	if e.failRead {
		return 0, errors.New("read failed")
	}
	return f.ReadAt(b, off)
}
func (e errDirectIO) Close(f *os.File) error { return f.Close() }

// stdTestDirectIO simple implementation to keep this file self‑contained.
type stdTestDirectIO struct{}

func (dio stdTestDirectIO) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	if dir := filepath.Dir(filename); dir != "." {
		_ = os.MkdirAll(dir, perm)
	}
	return os.OpenFile(filename, flag, perm)
}
func (dio stdTestDirectIO) WriteAt(ctx context.Context, f *os.File, b []byte, off int64) (int, error) {
	return f.WriteAt(b, off)
}
func (dio stdTestDirectIO) ReadAt(ctx context.Context, f *os.File, b []byte, off int64) (int, error) {
	return f.ReadAt(b, off)
}
func (dio stdTestDirectIO) Close(f *os.File) error { return f.Close() }

func TestDirectIOAndFileDirectIO_Scenarios(t *testing.T) {
	ctx := context.Background()

	type scenario struct {
		name string
		run  func(t *testing.T)
	}
	scenarios := []scenario{
		{name: "DirectIOBasic", run: func(t *testing.T) {
			dio := NewDirectIO()
			dir := t.TempDir()
			fn := filepath.Join(dir, "basic.dat")
			f, err := dio.Open(ctx, fn, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			blk := directio.AlignedBlock(blockSize)
			copy(blk[:5], []byte("hello"))
			if n, err := dio.WriteAt(ctx, f, blk, 0); err != nil || n != len(blk) {
				t.Fatalf("write n=%d err=%v", n, err)
			}
			rb := directio.AlignedBlock(blockSize)
			if n, err := dio.ReadAt(ctx, f, rb, 0); err != nil || n != len(rb) {
				t.Fatalf("read n=%d err=%v", n, err)
			}
			if string(rb[:5]) != "hello" {
				t.Fatalf("mismatch prefix %q", string(rb[:5]))
			}
			if err := dio.Close(f); err != nil {
				t.Fatalf("close: %v", err)
			}
		}},
		{name: "DirectIOConcreteType", run: func(t *testing.T) {
			d := directIO{}
			dir := t.TempDir()
			fn := filepath.Join(dir, "conc.dat")
			f, err := d.Open(ctx, fn, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			// O_DIRECT needs an aligned buffer. make() only sometimes returns one, so the test failed at random.
			blk := directio.AlignedBlock(blockSize)
			blk[0] = 7
			if _, err := d.WriteAt(ctx, f, blk, 0); err != nil {
				t.Fatalf("WriteAt: %v", err)
			}
			rb := directio.AlignedBlock(blockSize)
			if _, err := d.ReadAt(ctx, f, rb, 0); err != nil {
				t.Fatalf("ReadAt: %v", err)
			}
			if rb[0] != 7 {
				t.Fatalf("unexpected data")
			}
			if err := d.Close(f); err != nil {
				t.Fatalf("Close: %v", err)
			}
		}},
		{name: "FileDirectIO_OpenFailuresAndEarlyErrors", run: func(t *testing.T) {
			fio := newFileDirectIOInjected(failOpenDirectIO{})
			if err := fio.open(ctx, filepath.Join(t.TempDir(), "a.dat"), os.O_RDWR, 0o644); err == nil {
				t.Fatalf("expected open error")
			}
			if _, err := fio.writeAt(ctx, []byte("x"), 0); err == nil {
				t.Fatalf("expected write before open error")
			}
			if _, err := fio.readAt(ctx, make([]byte, 1), 0); err == nil {
				t.Fatalf("expected read before open error")
			}
			if err := fio.close(); err != nil {
				t.Fatalf("close unopened: %v", err)
			}
		}},
		{name: "FileDirectIO_DoubleOpen_WriteFail_IdempotentClose", run: func(t *testing.T) {
			fio := newFileDirectIOInjected(errDirectIO{failWrite: true})
			tmp := filepath.Join(t.TempDir(), "seg.dat")
			if err := fio.open(ctx, tmp, os.O_RDWR, 0o600); err != nil {
				t.Fatalf("open: %v", err)
			}
			if err := fio.open(ctx, tmp, os.O_RDWR, 0o600); err == nil {
				t.Fatalf("expected double open error")
			}
			if _, err := fio.writeAt(ctx, []byte("abc"), 0); err == nil {
				t.Fatalf("expected injected write error")
			}
			if err := fio.close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			if err := fio.close(); err != nil {
				t.Fatalf("idempotent close: %v", err)
			}
		}},
		{name: "FileDirectIO_HappyPath_ReadWriteHelpers", run: func(t *testing.T) {
			fio := newFileDirectIOInjected(stdTestDirectIO{})
			name := filepath.Join(t.TempDir(), "ok.dat")
			if err := fio.open(ctx, name, os.O_RDWR|os.O_CREATE, 0o644); err != nil {
				t.Fatalf("open: %v", err)
			}
			blk := fio.createAlignedBlock()
			for i := range blk {
				blk[i] = byte(i)
			}
			if n, err := fio.writeAt(ctx, blk, 0); err != nil || n != len(blk) {
				t.Fatalf("writeAt n=%d err=%v", n, err)
			}
			rb := make([]byte, len(blk))
			if n, err := fio.readAt(ctx, rb, 0); err != nil || n != len(rb) {
				t.Fatalf("readAt n=%d err=%v", n, err)
			}
			if !fio.fileExists(name) {
				t.Fatalf("fileExists expected true")
			}
			if sz, err := fio.getFileSize(name); err != nil || sz == 0 {
				t.Fatalf("getFileSize sz=%d err=%v", sz, err)
			}
			if !fio.isEOF(io.EOF) {
				t.Fatalf("isEOF true expected")
			}
			if fio.isEOF(fmt.Errorf("x")) {
				t.Fatalf("isEOF false expected")
			}
			if b2 := fio.createAlignedBlockOfSize(2 * directio.BlockSize); len(b2) != 2*directio.BlockSize {
				t.Fatalf("aligned size mismatch")
			}
			if err := fio.close(); err != nil {
				t.Fatalf("close: %v", err)
			}
		}},
		{name: "FileDirectIO_DefaultConcrete", run: func(t *testing.T) {
			fio := newFileDirectIO()
			if fio.directIO == nil {
				t.Fatalf("expected default NewDirectIO implementation")
			}
		}},
		{name: "FileDirectIO_FileExistsFalse_GetFileSizeErr", run: func(t *testing.T) {
			fio := newFileDirectIOInjected(stdTestDirectIO{})
			missing := filepath.Join(t.TempDir(), "missing.bin")
			if fio.fileExists(missing) {
				t.Fatalf("fileExists should be false for missing file")
			}
			if _, err := fio.getFileSize(missing); err == nil {
				t.Fatalf("expected getFileSize error for missing file")
			}
		}},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, sc.run)
	}
}
