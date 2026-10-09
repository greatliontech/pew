package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) lock(ctx context.Context) (func(), error) {
	if err := s.ensureDir(s.Root); err != nil {
		return nil, err
	}
	p := filepath.Join(s.Root, ".pew-lock")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600|os.ModeExclusive)
		if errors.Is(err, os.ErrExist) {
			if err := checkRegularFile(p); err != nil {
				return nil, err
			}
			f, err = os.OpenFile(p, os.O_RDWR, 0)
		}
		if err == nil {
			info, err := f.Stat()
			if err != nil || info.Mode()&os.ModeExclusive == 0 {
				f.Close()
				return nil, fmt.Errorf("store: lock lacks exclusive-open mode: %v", err)
			}
			return func() { _ = f.Close() }, nil
		}
		if !strings.Contains(err.Error(), "exclusive") && !strings.Contains(err.Error(), "in use") {
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
