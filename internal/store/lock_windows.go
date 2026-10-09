package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func (s *Store) lock(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.ensureDir(s.Root); err != nil {
		return nil, err
	}
	p := filepath.Join(s.Root, ".pew-lock")
	if err := checkRegularFile(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("store: invalid lock file: %v", err)
	}
	var overlap windows.Overlapped
	for {
		err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
		if err == nil {
			if err := ctx.Err(); err != nil {
				windows.CloseHandle(h)
				return nil, err
			}
			return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, &overlap); _ = windows.CloseHandle(h) }, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			windows.CloseHandle(h)
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			windows.CloseHandle(h)
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
