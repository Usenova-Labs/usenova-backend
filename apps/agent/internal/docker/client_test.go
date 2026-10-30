package docker

import (
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
)

func writeFrame(w io.Writer, stream stdcopy.StdType, payload []byte) {
	header := make([]byte, 8)
	header[0] = byte(stream)
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

// TestDemuxLogStream merges the multiplexed stdout/stderr frames Docker returns
// for non-TTY containers into a single plain-text stream, and forwards a clean
// EOF when the source ends.
func TestDemuxLogStream(t *testing.T) {
	var buf strings.Builder
	writeFrame(&buf, stdcopy.Stdout, []byte("hello\n"))
	writeFrame(&buf, stdcopy.Stderr, []byte("oops\n"))

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
	writeFrame(&buf, stdcopy.Stdout, []byte("tail"))

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
