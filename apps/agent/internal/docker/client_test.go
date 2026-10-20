package docker

import (
	"io"
	"strings"
	"testing"

	"github.com/docker/docker/pkg/stdcopy"
)

// TestDemuxLogStream merges the multiplexed stdout/stderr frames Docker returns
// for non-TTY containers into a single plain-text stream, and forwards a clean
// EOF when the source ends.
func TestDemuxLogStream(t *testing.T) {
	var buf strings.Builder
	out := stdcopy.NewStdWriter(&buf, stdcopy.Stdout)
	errw := stdcopy.NewStdWriter(&buf, stdcopy.Stderr)
	_, _ = out.Write([]byte("hello\n"))
	_, _ = errw.Write([]byte("oops\n"))

	rc := demuxLogStream(io.NopCloser(strings.NewReader(buf.String())))
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello\noops\n" {
		t.Errorf("demuxed = %q, want %q", got, "hello\noops\n")
	}
}

// TestDemuxLogStreamCleanEOF ensures a normal end of stream surfaces as EOF,
// not as an opaque error.
func TestDemuxLogStreamCleanEOF(t *testing.T) {
	var buf strings.Builder
	_, _ = stdcopy.NewStdWriter(&buf, stdcopy.Stdout).Write([]byte("tail"))

	rc := demuxLogStream(io.NopCloser(strings.NewReader(buf.String())))
	defer rc.Close()

	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(b) != "tail" {
		t.Errorf("got %q, want %q", b, "tail")
	}
}
