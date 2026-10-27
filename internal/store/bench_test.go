package store

import (
	"bytes"
	"testing"
)

func BenchmarkParseRecording(b *testing.B) {
	raw := []byte(sample)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		recs, err := Parse(bytes.NewReader(raw), "BenchmarkRun")
		if err != nil || len(recs) == 0 {
			b.Fatalf("Parse = (%d, %v)", len(recs), err)
		}
	}
}
