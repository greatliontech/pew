package store

import (
	"testing"

	"github.com/greatliontech/pew/internal/run"
)

// Vouches live in the native payload, with a display projection but no parallel
// recording key. The projection vocabulary cannot enlarge the file's closed set.
func TestRecordingConfigKeyAdmitsVouches(t *testing.T) {
	if run.IsRecordingKey("pew-vouches") || !run.IsFingerprintProjection("pew-vouches") {
		t.Fatal("vouches must be a native-payload projection, never a parallel recording key")
	}
	if run.IsRecordingKey("pew-unknown") {
		t.Fatal("unknown pew key admitted")
	}
}
