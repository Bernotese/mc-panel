package files

// DiskSpace reports the free space of the file system that holds dir.
type DiskSpace interface {
	Available(dir string) (uint64, error)
}

// statfsDisk asks the kernel (statfs: blocks available to unprivileged users × block size).
type statfsDisk struct{}

func (statfsDisk) Available(dir string) (uint64, error) { return availableBytes(dir) }
