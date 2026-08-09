package store

import "testing"

// pew-vouches is a recognized recording key: a vouched recording must
// never read as stale-format - the write path emits the line, so the
// read path admits it (spec §5).
func TestRecordingConfigKeyAdmitsVouches(t *testing.T) {
	if !recordingConfigKey("pew-vouches") {
		t.Fatal("pew-vouches refused as a recording config key")
	}
	if recordingConfigKey("pew-unknown") {
		t.Fatal("unknown pew key admitted")
	}
}
