//go:build aix || solaris || illumos

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// POSIX record locks are process-owned. The process semaphore prevents another
// local holder from opening/closing the lock inode and releasing a live lock.
var publicationPermit = make(chan struct{}, 1)

func (s *Store) lock(ctx context.Context) (func(), error) {
	select {
	case publicationPermit <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-publicationPermit }
	if err := s.ensureDir(s.Root); err != nil {
		release()
		return nil, err
	}
	p := filepath.Join(s.Root, ".pew-lock")
	fd, err := unix.Open(p, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		release()
		return nil, err
	}
	f := os.NewFile(uintptr(fd), p)
	close := func() { _ = f.Close(); release() }
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		close()
		if err == nil {
			err = errors.New("store: lock is not a regular file")
		}
		return nil, err
	}
	lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0, Start: 0, Len: 1}
	for {
		err = unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lock)
		if err == nil {
			if err := ctx.Err(); err != nil {
				close()
				return nil, err
			}
			return close, nil
		}
		if !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
