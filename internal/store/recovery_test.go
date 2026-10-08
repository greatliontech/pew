package store

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func FuzzCorruptRecordingNeverBecomesEvidence(f *testing.F) {
	f.Add([]byte("BenchmarkProbe-8 1 5 ns/op\n"), true)
	f.Add([]byte("pew- log text: value\n"), false)
	f.Add([]byte(strings.Repeat("x", bufio.MaxScanTokenSize+1)), true)
	f.Fuzz(func(t *testing.T, payload []byte, marked bool) {
		// Only the chosen header supplies namespace ownership. The arbitrary
		// body still exercises malformed rows, line lengths and byte encodings.
		body := bytes.ReplaceAll(payload, []byte("pew-"), []byte("foreign-"))
		var input bytes.Buffer
		if marked {
			input.WriteString("pew-format: 2\n")
		}
		input.Write(body)
		input.WriteString("\nBenchmarkBroken not-a-count 5 ns/op\n")
		rows, err := Parse(&input, "corrupt")
		if err == nil || rows != nil || errors.Is(err, ErrInvalidRecording) != marked {
			t.Fatalf("marked=%v: rows=%v, err=%v", marked, rows, err)
		}
	})
}

func TestParseIdentifiesUnusablePewRecordings(t *testing.T) {
	for name, text := range map[string]string{
		"oversized legacy": "pew-format: 2\npew-test-variant-ledger: " + strings.Repeat("A", bufio.MaxScanTokenSize+1) + "\nBenchmarkProbe-8 1 5 ns/op\n",
		"current syntax":   "pew-format: 3\nBenchmarkProbe-8 not-a-count 5 ns/op\n",
		"broken chunks":    "pew-format: 4\npew-fingerprint.2: abc\nBenchmarkProbe-8 1 5 ns/op\n",
		"no samples":       "pew-format: 3\n",
		"valid prefix":     "pew-format: 3\nBenchmarkProbe-8 1 5 ns/op\nBenchmarkProbe-8 not-a-count 5 ns/op\n",
		"tab separator":    "pew-format:\t2\nBenchmarkProbe-8 not-a-count 5 ns/op\n",
	} {
		t.Run(name, func(t *testing.T) {
			rows, err := Parse(strings.NewReader(text), name)
			if !errors.Is(err, ErrInvalidRecording) || rows != nil {
				t.Fatalf("unusable known recording returned rows=%v, err=%v", rows, err)
			}
			if name == "oversized legacy" && !errors.Is(err, bufio.ErrTooLong) {
				t.Fatalf("scanner cause lost: %v", err)
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("unrelated error identity claimed: %v", err)
			}
		})
	}
	for _, prefix := range []string{"goos: linux\n", "pew-UPPER: value\n", "pew-key:value\n", "pew-key:\n"} {
		rows, err := Parse(strings.NewReader(prefix+"pew-format: 2\nBenchmarkProbe-8 not-a-count 5 ns/op\n"), "marker-after-prefix")
		if !errors.Is(err, ErrInvalidRecording) || rows != nil {
			t.Fatalf("later ownership marker lost after %q: %v", prefix, err)
		}
	}
}

type failingRecordingReader struct{ err error }

func (r failingRecordingReader) Read([]byte) (int, error) { return 0, r.err }

func TestParseDoesNotAuthorizeReplacingUnknownContent(t *testing.T) {
	for _, prefix := range []string{"", "note: value\n", "pew- is only log text: value\n", "pew-not-a-config-line\n", "pew-UPPER: value\n", "pew-\u00a0name: value\n", "pew-key:value\n", "pew-key:\n", "pew-key: \t\n", "pew-key: \r\n"} {
		rows, err := Parse(strings.NewReader(prefix+"BenchmarkProbe-8 not-a-count 5 ns/op\n"), "foreign")
		if err == nil || errors.Is(err, ErrInvalidRecording) || rows != nil {
			t.Fatalf("foreign corruption %q: rows=%v, err=%v", prefix, rows, err)
		}
	}
	cause := errors.New("read failed after marker")
	rows, err := Parse(io.MultiReader(strings.NewReader("pew-format: 2\n"), failingRecordingReader{cause}), "unreadable")
	if !errors.Is(err, cause) || errors.Is(err, ErrInvalidRecording) || rows != nil {
		t.Fatalf("partial read authorized replacement: rows=%v, err=%v", rows, err)
	}
}
