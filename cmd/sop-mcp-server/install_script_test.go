package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests run scripts/install.sh against a local server that plays the
// release host. They check how the installer fails: it has to stop with a plain
// message and leave nothing new, and a failed upgrade has to keep the binary
// that already works.

type fakeRelease struct {
	gz      []byte // what the .gz asset serves
	sumsFor []byte // the bytes the checksum file vouches for
	noSums  bool
}

func newRelease(t *testing.T, script string) fakeRelease {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(script))
	zw.Close()
	return fakeRelease{gz: buf.Bytes(), sumsFor: buf.Bytes()}
}

func (r fakeRelease) serve(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256(r.sumsFor)
	var sums strings.Builder
	for _, goos := range []string{"darwin", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			fmt.Fprintf(&sums, "%x  sop-mcp-server-%s-%s.gz\n", sum, goos, arch)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "SHA256SUMS") && !r.noSums:
			w.Write([]byte(sums.String()))
		case strings.HasSuffix(req.URL.Path, ".gz"):
			w.Write(r.gz)
		default:
			http.NotFound(w, req)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func runInstaller(t *testing.T, base, dest string) (string, error) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("install.sh supports macOS and Linux")
	}
	if _, err := exec.LookPath("gzip"); err != nil {
		t.Skip("gzip not available")
	}
	cmd := exec.Command("sh", filepath.Join("..", "..", "scripts", "install.sh"))
	cmd.Env = append(os.Environ(),
		"JOLTRIN_BASE_URL="+base,
		"JOLTRIN_BIN_DIR="+dest,
		"JOLTRIN_NO_SETUP=1",
		"HOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallerInstallsAGoodRelease(t *testing.T) {
	base := newRelease(t, "#!/bin/sh\necho good\n").serve(t)
	dest := filepath.Join(t.TempDir(), "bin")
	out, err := runInstaller(t, base, dest)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dest, "sop-mcp-server"))
	if err != nil || !strings.Contains(string(got), "echo good") {
		t.Fatalf("binary not installed: %v %q", err, got)
	}
	if st, _ := os.Stat(filepath.Join(dest, "sop-mcp-server")); st.Mode()&0o100 == 0 {
		t.Error("installed binary is not executable")
	}
}

func TestInstallerFailsClosed(t *testing.T) {
	good := newRelease(t, "#!/bin/sh\necho good\n")

	tampered := good
	tampered.gz = append([]byte{}, good.gz...)
	tampered.gz[len(tampered.gz)/2] ^= 0xff

	truncated := good
	truncated.gz = good.gz[:len(good.gz)/2]
	truncated.sumsFor = truncated.gz // checksum passes, unpacking must still fail

	cases := map[string]struct {
		rel  fakeRelease
		want string
	}{
		"checksum does not match": {tampered, "checksum does not match"},
		"truncated download":      {truncated, "could not unpack"},
		"no checksum file":        {fakeRelease{gz: good.gz, noSums: true}, "download failed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "bin")
			out, err := runInstaller(t, c.rel.serve(t), dest)
			if err == nil {
				t.Fatalf("installer succeeded:\n%s", out)
			}
			if !strings.Contains(out, "install: "+c.want) {
				t.Errorf("want message %q, got:\n%s", c.want, out)
			}
			if _, statErr := os.Stat(filepath.Join(dest, "sop-mcp-server")); statErr == nil {
				t.Error("a binary was installed after a failure")
			}
		})
	}
}

func TestFailedUpgradeKeepsTheWorkingBinary(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "bin")
	if out, err := runInstaller(t, newRelease(t, "#!/bin/sh\necho v1\n").serve(t), dest); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	bad := newRelease(t, "#!/bin/sh\necho v2\n")
	bad.sumsFor = []byte("something else") // checksum will not match
	if _, err := runInstaller(t, bad.serve(t), dest); err == nil {
		t.Fatal("tampered upgrade succeeded")
	}
	got, err := os.ReadFile(filepath.Join(dest, "sop-mcp-server"))
	if err != nil || !strings.Contains(string(got), "echo v1") {
		t.Fatalf("working binary was lost: %v %q", err, got)
	}
}

func TestInstallerExplainsAnUnwritableFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	base := newRelease(t, "#!/bin/sh\necho good\n").serve(t)
	out, err := runInstaller(t, base, filepath.Join(ro, "bin"))
	if err == nil {
		t.Fatalf("installer wrote into a read-only folder:\n%s", out)
	}
	if !strings.Contains(out, "install: cannot write to") || !strings.Contains(out, "JOLTRIN_BIN_DIR") {
		t.Errorf("message does not say what to do:\n%s", out)
	}
}
