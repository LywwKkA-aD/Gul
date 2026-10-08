//go:build !windows

package catalog

import (
	"golang.org/x/sys/unix"
	"os"
)

func acquireFileLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, unavailable
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		_ = file.Close()
		return nil, unavailable
	}
	return file, nil
}
