package bench

import (
	"testing"

	_ "github.com/thegrumpylion/pew/internal/closure/fixtures/linknamelocal/target"
	_ "unsafe"
)

//go:linkname hidden github.com/thegrumpylion/pew/internal/closure/fixtures/linknamelocal/target.Hidden
func hidden() int

func BenchmarkLinknameLocal(b *testing.B) {
	for b.Loop() {
		_ = hidden()
	}
}
