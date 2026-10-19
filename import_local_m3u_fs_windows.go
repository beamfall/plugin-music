//go:build windows

package music

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const localM3UWindowsRemoteProtocolInfoBytes = 116 // sizeof(FILE_REMOTE_PROTOCOL_INFO)

type localM3UWindowsRootHandle interface {
	Close() error
	Lstat(string) (os.FileInfo, error)
	Open(string) (*os.File, error)
}

type localM3UWindowsRootInfo struct {
	directory bool
	remote    bool
}

var (
	localM3UWindowsOpenRoot    = localM3UWindowsOpenRootSystem
	localM3UWindowsDriveType   = localM3UWindowsDriveTypeSystem
	localM3UWindowsInspectRoot = localM3UWindowsInspectRootSystem
)

type localM3URoot struct {
	root     localM3UWindowsRootHandle
	rootPath string
	lifetime localM3URootLifetime
}

func openLocalM3URoot(path string) (*localM3URoot, error) {
	volume := filepath.VolumeName(path)
	if !filepath.IsAbs(path) || len(volume) != 2 || volume[1] != ':' {
		return nil, newLocalM3UError("resolve root", path, ErrLocalM3UOutsideRoot, nil)
	}
	driveType, err := localM3UWindowsDriveType(path)
	if err != nil {
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UInvalidPath, err)
	}
	if driveType == windows.DRIVE_REMOTE {
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UOutsideRoot, nil)
	}
	if !localM3UWindowsLocalDrive(driveType) {
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UInvalidPath, nil)
	}
	root, err := localM3UWindowsOpenRoot(path)
	if err != nil {
		return nil, newLocalM3UError("open root", path, ErrLocalM3UInvalidPath, err)
	}
	info, err := localM3UWindowsInspectRoot(root)
	if err != nil {
		_ = root.Close()
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UInvalidPath, err)
	}
	if !info.directory {
		_ = root.Close()
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UNotRegular, nil)
	}
	if info.remote {
		_ = root.Close()
		return nil, newLocalM3UError("stat root", path, ErrLocalM3UOutsideRoot, nil)
	}
	return &localM3URoot{root: root, rootPath: path}, nil
}

func (r *localM3URoot) path() string { return r.rootPath }

func (r *localM3URoot) Close() error { return r.lifetime.close(r.closeHandle) }

func (r *localM3URoot) closeHandle() error { return r.root.Close() }

func (r *localM3URoot) openRegular(relativePath string, maxBytes int64) (*os.File, error) {
	if err := validateLocalM3URelativePath(relativePath); err != nil {
		return nil, err
	}
	fullPath := filepath.Join(r.rootPath, relativePath)
	if strings.Contains(relativePath, ":") {
		return nil, newLocalM3UError("resolve", fullPath, ErrLocalM3UInvalidPath, nil)
	}
	if err := r.rejectReparsePoints(relativePath, fullPath); err != nil {
		return nil, err
	}

	f, err := r.root.Open(relativePath)
	if err != nil {
		return nil, newLocalM3UError("open", fullPath, ErrLocalM3UInvalidPath, err)
	}
	closeOnError := func(kind, cause error) (*os.File, error) {
		_ = f.Close()
		return nil, newLocalM3UError("stat", fullPath, kind, cause)
	}
	info, err := f.Stat()
	if err != nil {
		return closeOnError(ErrLocalM3UInvalidPath, err)
	}
	if localM3UWindowsReparsePoint(info) {
		return closeOnError(ErrLocalM3USymlink, nil)
	}
	if !info.Mode().IsRegular() {
		return closeOnError(ErrLocalM3UNotRegular, nil)
	}
	if info.Size() > maxBytes {
		return closeOnError(ErrLocalM3UTooLarge, nil)
	}
	return f, nil
}

func (r *localM3URoot) rejectReparsePoints(relativePath, fullPath string) error {
	checkedPath := ""
	for _, component := range strings.Split(relativePath, string(filepath.Separator)) {
		checkedPath = filepath.Join(checkedPath, component)
		info, err := r.root.Lstat(checkedPath)
		if err != nil {
			return newLocalM3UError("open", fullPath, ErrLocalM3UInvalidPath, err)
		}
		if localM3UWindowsReparsePoint(info) {
			return newLocalM3UError("open", fullPath, ErrLocalM3USymlink, nil)
		}
	}
	return nil
}

func localM3UWindowsReparsePoint(info os.FileInfo) bool {
	return info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
}

func localM3UWindowsOpenRootSystem(path string) (localM3UWindowsRootHandle, error) {
	return os.OpenRoot(path)
}

func localM3UWindowsDriveTypeSystem(path string) (uint32, error) {
	volumeRoot, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
	if err != nil {
		return windows.DRIVE_UNKNOWN, err
	}
	return windows.GetDriveType(volumeRoot), nil
}

func localM3UWindowsLocalDrive(driveType uint32) bool {
	switch driveType {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_CDROM, windows.DRIVE_RAMDISK:
		return true
	default:
		return false
	}
}

func localM3UWindowsInspectRootSystem(root localM3UWindowsRootHandle) (localM3UWindowsRootInfo, error) {
	handle, err := root.Open(".")
	if err != nil {
		return localM3UWindowsRootInfo{}, err
	}
	defer func() { _ = handle.Close() }()
	info, err := handle.Stat()
	if err != nil {
		return localM3UWindowsRootInfo{}, err
	}
	remote, err := localM3UWindowsRemoteHandle(windows.Handle(handle.Fd()))
	if err != nil {
		return localM3UWindowsRootInfo{}, err
	}
	return localM3UWindowsRootInfo{directory: info.IsDir(), remote: remote}, nil
}

func localM3UWindowsRemoteHandle(handle windows.Handle) (bool, error) {
	var protocolInfo [localM3UWindowsRemoteProtocolInfoBytes]byte
	err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileRemoteProtocolInfo,
		&protocolInfo[0],
		uint32(len(protocolInfo)),
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_INVALID_FUNCTION) ||
		errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	return false, err
}
