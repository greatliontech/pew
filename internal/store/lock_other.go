//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !windows && !aix && !solaris && !illumos && !plan9

package store

import (
	"context"
	"fmt"
)

func (s *Store) lock(ctx context.Context) (func(), error) {
	return nil, fmt.Errorf("store: cross-process publication locking is unsupported on this platform")
}
