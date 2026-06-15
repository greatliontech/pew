package codec

import "github.com/thegrumpylion/pew/internal/closure/fixtures/initregistry/registry"

type gz struct{}

func (gz) Decode(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func init() {
	registry.Register("gzip", gz{})
}
