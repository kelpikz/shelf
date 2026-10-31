//go:build linux || darwin

package sites

import "golang.org/x/sys/unix"

func diskFree(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return availableBytes(stat.Bavail, uint64(stat.Bsize)), nil
}
