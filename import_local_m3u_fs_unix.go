//go:build unix

package music

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

const localM3UUnixMountLocal = 0x1000

type localM3UUnixRootInfo struct {
	mode   uint32
	remote bool
}

var (
	localM3UUnixOpen        = localM3UUnixOpenSystem
	localM3UUnixOpenat      = localM3UUnixOpenatSystem
	localM3UUnixInspectRoot = localM3UUnixInspectRootSystem
)

type localM3URoot struct {
	dir      *os.File
	rootPath string
	lifetime localM3URootLifetime
}

func openLocalM3URoot(path string) (*localM3URoot, error) {
	fd, err := localM3UUnixOpen(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NONBLOCK)
	if err != nil {
		return nil, localM3UUnixError("open root", path, err)
	}
	info, err := localM3UUnixInspectRoot(fd)
	if err != nil {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UInvalidPath, err)
	}
	if info.mode != unix.S_IFDIR {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UNotRegular, nil)
	}
	if info.remote {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UOutsideRoot, nil)
	}
	dir := os.NewFile(uintptr(fd), path)
	if dir == nil {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("open root", path, ErrLocalM3UInvalidPath, nil)
	}
	return &localM3URoot{dir: dir, rootPath: path}, nil
}

func (r *localM3URoot) path() string { return r.rootPath }

func (r *localM3URoot) Close() error { return r.lifetime.close(r.closeHandle) }

func (r *localM3URoot) closeHandle() error { return r.dir.Close() }

func (r *localM3URoot) openRegular(relativePath string, maxBytes int64) (*os.File, error) {
	if err := validateLocalM3URelativePath(relativePath); err != nil {
		return nil, err
	}
	components := strings.Split(relativePath, string(filepath.Separator))
	dirFD := int(r.dir.Fd())
	ownedDirFD := -1
	for _, component := range components[:len(components)-1] {
		nextFD, err := localM3UUnixOpenat(
			dirFD,
			component,
			unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK,
		)
		if err != nil {
			openErr := localM3UUnixOpenatError(dirFD, component, filepath.Join(r.rootPath, relativePath), err)
			if ownedDirFD >= 0 {
				_ = unix.Close(ownedDirFD)
			}
			runtime.KeepAlive(r.dir)
			return nil, openErr
		}
		if ownedDirFD >= 0 {
			_ = unix.Close(ownedDirFD)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(nextFD, &stat); err != nil {
			_ = unix.Close(nextFD)
			runtime.KeepAlive(r.dir)
			return nil, newLocalM3UError("stat", filepath.Join(r.rootPath, relativePath), ErrLocalM3UInvalidPath, err)
		}
		if uint32(stat.Mode)&unix.S_IFMT != unix.S_IFDIR {
			_ = unix.Close(nextFD)
			runtime.KeepAlive(r.dir)
			return nil, newLocalM3UError("stat", filepath.Join(r.rootPath, relativePath), ErrLocalM3UNotRegular, nil)
		}
		dirFD = nextFD
		ownedDirFD = nextFD
	}

	fd, err := localM3UUnixOpenat(
		dirFD,
		components[len(components)-1],
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK,
	)
	fullPath := filepath.Join(r.rootPath, relativePath)
	if err != nil {
		openErr := localM3UUnixOpenatError(
			dirFD,
			components[len(components)-1],
			fullPath,
			err,
		)
		if ownedDirFD >= 0 {
			_ = unix.Close(ownedDirFD)
		}
		runtime.KeepAlive(r.dir)
		return nil, openErr
	}
	if ownedDirFD >= 0 {
		_ = unix.Close(ownedDirFD)
	}
	runtime.KeepAlive(r.dir)

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("stat", fullPath, ErrLocalM3UInvalidPath, err)
	}
	if uint32(stat.Mode)&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("stat", fullPath, ErrLocalM3UNotRegular, nil)
	}
	if stat.Size < 0 || stat.Size > maxBytes {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("stat", fullPath, ErrLocalM3UTooLarge, nil)
	}
	// The descriptor's post-open identity is authoritative. Link count is
	// intentionally irrelevant: an in-root hardlink names this regular file.
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("configure", fullPath, ErrLocalM3UInvalidPath, err)
	}
	f := os.NewFile(uintptr(fd), fullPath)
	if f == nil {
		_ = unix.Close(fd)
		return nil, newLocalM3UError("open", fullPath, ErrLocalM3UInvalidPath, nil)
	}
	return f, nil
}

func localM3UUnixOpenSystem(path string, flags int) (int, error) {
	for {
		fd, err := unix.Open(path, flags, 0)
		if !errors.Is(err, unix.EINTR) {
			return fd, err
		}
	}
}

func localM3UUnixOpenatSystem(dirFD int, path string, flags int) (int, error) {
	for {
		fd, err := unix.Openat(dirFD, path, flags, 0)
		if !errors.Is(err, unix.EINTR) {
			return fd, err
		}
	}
}

func localM3UUnixInspectRootSystem(fd int) (localM3UUnixRootInfo, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return localM3UUnixRootInfo{}, err
	}
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(fd, &filesystem); err != nil {
		return localM3UUnixRootInfo{}, err
	}
	return localM3UUnixRootInfo{
		mode:   uint32(stat.Mode) & unix.S_IFMT,
		remote: localM3UUnixRemoteFilesystem(&filesystem),
	}, nil
}

func localM3UUnixRemoteFilesystem(filesystem *unix.Statfs_t) bool {
	value := reflect.ValueOf(filesystem).Elem()
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		if flags, ok := localM3UUnixStatfsUint(value, "Flags", "F_flags"); ok {
			return flags&localM3UUnixMountLocal == 0
		}
	}
	name := strings.ToLower(localM3UUnixStatfsName(value, "Fstypename", "F_fstypename"))
	if localM3UUnixRemoteFilesystemName(name) {
		return true
	}
	filesystemType, ok := localM3UUnixStatfsUint(value, "Type", "F_type")
	return ok && localM3UUnixRemoteFilesystemType(filesystemType)
}

func localM3UUnixStatfsUint(value reflect.Value, names ...string) (uint64, bool) {
	field := localM3UUnixStatfsField(value, names...)
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if field.Type().Bits() == 32 {
			return uint64(uint32(field.Int())), true
		}
		return uint64(field.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return field.Uint(), true
	default:
		return 0, false
	}
}

func localM3UUnixStatfsName(value reflect.Value, names ...string) string {
	field := localM3UUnixStatfsField(value, names...)
	if field.Kind() != reflect.Array || field.Type().Elem().Kind() != reflect.Uint8 {
		return ""
	}
	return strings.TrimRight(string(field.Slice(0, field.Len()).Bytes()), "\x00")
}

func localM3UUnixStatfsField(value reflect.Value, names ...string) reflect.Value {
	for _, name := range names {
		field := value.FieldByName(name)
		if field.IsValid() {
			return field
		}
	}
	return reflect.Value{}
}

func localM3UUnixRemoteFilesystemName(name string) bool {
	switch name {
	case "9p", "afpfs", "ceph", "cifs", "fuse.sshfs", "nfs", "nfs4", "smbfs", "sshfs", "webdav":
		return true
	default:
		return false
	}
}

func localM3UUnixRemoteFilesystemType(filesystemType uint64) bool {
	switch filesystemType {
	case 0x01021997, // 9P_SUPER_MAGIC
		0x0000517b, // SMB_SUPER_MAGIC
		0x0000564c, // NCP_SUPER_MAGIC
		0x00006969, // NFS_SUPER_MAGIC
		0x00c36400, // CEPH_SUPER_MAGIC
		0x0bd00bd0, // LUSTRE_SUPER_MAGIC
		0x5346414f, // AFS_SUPER_MAGIC
		0x73757245, // CODA_SUPER_MAGIC
		0xfe534d42, // SMB2_SUPER_MAGIC
		0xff534d42: // CIFS_MAGIC_NUMBER
		return true
	default:
		return false
	}
}

func localM3UUnixError(op, path string, err error) error {
	kind := ErrLocalM3UInvalidPath
	if errors.Is(err, unix.ELOOP) {
		kind = ErrLocalM3USymlink
	}
	return newLocalM3UError(op, path, kind, err)
}

func localM3UUnixOpenatError(dirFD int, component, path string, err error) error {
	kind := ErrLocalM3UInvalidPath
	var stat unix.Stat_t
	statErr := unix.Fstatat(dirFD, component, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if statErr == nil && uint32(stat.Mode)&unix.S_IFMT == unix.S_IFLNK {
		kind = ErrLocalM3USymlink
	} else if errors.Is(err, unix.ELOOP) {
		kind = ErrLocalM3USymlink
	}
	return newLocalM3UError("open", path, kind, err)
}
