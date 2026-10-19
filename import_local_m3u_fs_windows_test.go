//go:build windows

package music

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var localM3UGetProcessHandleCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")

func TestLocalM3UWindowsRejectsPlaylistAndMediaReparsePoints(t *testing.T) {
	t.Run("playlist", func(t *testing.T) {
		dir := t.TempDir()
		realPlaylist := writeLocalM3U(t, dir, "track.mp3\n")
		if err := os.WriteFile(filepath.Join(dir, "track.mp3"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		linkedPlaylist := filepath.Join(dir, "linked.m3u")
		localM3UWindowsSymlink(t, filepath.Base(realPlaylist), linkedPlaylist)
		_, err := NewLocalM3USource(linkedPlaylist).Items(context.Background())
		requireLocalM3UError(t, err, ErrLocalM3USymlink)
	})

	t.Run("final media component", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.mp3")
		if err := os.WriteFile(outside, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		localM3UWindowsSymlink(t, outside, filepath.Join(root, "linked.mp3"))
		playlist := writeLocalM3U(t, root, "linked.mp3\n")
		_, err := NewLocalM3USource(playlist).Items(context.Background())
		requireLocalM3UError(t, err, ErrLocalM3USymlink)
	})

	t.Run("intermediate media component", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "outside.mp3"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		localM3UWindowsSymlink(t, outside, filepath.Join(root, "linked"))
		playlist := writeLocalM3U(t, root, filepath.Join("linked", "outside.mp3")+"\n")
		_, err := NewLocalM3USource(playlist).Items(context.Background())
		requireLocalM3UError(t, err, ErrLocalM3USymlink)
	})
}

func TestLocalM3UWindowsRejectsDeviceAndNetworkPathsPromptly(t *testing.T) {
	dir := t.TempDir()
	playlist := writeLocalM3U(t, dir, "NUL\n")
	started := time.Now()
	_, err := NewLocalM3USource(playlist).Items(context.Background())
	if time.Since(started) > time.Second {
		t.Fatalf("device rejection took %s, want under 1s", time.Since(started))
	}
	var typed *LocalM3UError
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want *LocalM3UError: %v", err, err)
	}

	_, _, err = localM3UMediaPath(dir, `\\server\share\track.mp3`)
	requireLocalM3UError(t, err, ErrLocalM3UOutsideRoot)
	_, _, err = localM3UMediaPath(dir, `C:volume-relative.mp3`)
	requireLocalM3UError(t, err, ErrLocalM3UOutsideRoot)
	_, err = openLocalM3URoot(`\\server\share`)
	requireLocalM3UError(t, err, ErrLocalM3UOutsideRoot)
}

func TestLocalM3UWindowsRootClassification(t *testing.T) {
	localPath := t.TempDir()
	tests := []struct {
		name       string
		path       string
		driveType  uint32
		info       localM3UWindowsRootInfo
		want       error
		wantOpened bool
	}{
		{
			name:       "fixed local root accepted",
			path:       localPath,
			driveType:  windows.DRIVE_FIXED,
			info:       localM3UWindowsRootInfo{directory: true},
			wantOpened: true,
		},
		{
			name:      "mapped remote drive rejected",
			path:      localPath,
			driveType: windows.DRIVE_REMOTE,
			want:      ErrLocalM3UOutsideRoot,
		},
		{
			name:       "remote root handle rejected",
			path:       localPath,
			driveType:  windows.DRIVE_FIXED,
			info:       localM3UWindowsRootInfo{directory: true, remote: true},
			want:       ErrLocalM3UOutsideRoot,
			wantOpened: true,
		},
		{
			name:       "non-directory root rejected",
			path:       localPath,
			driveType:  windows.DRIVE_FIXED,
			want:       ErrLocalM3UNotRegular,
			wantOpened: true,
		},
		{
			name:      "UNC root rejected",
			path:      `\\server\share`,
			driveType: windows.DRIVE_REMOTE,
			want:      ErrLocalM3UOutsideRoot,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalOpenRoot := localM3UWindowsOpenRoot
			originalDriveType := localM3UWindowsDriveType
			originalInspectRoot := localM3UWindowsInspectRoot
			fakeRoot := newLocalM3UWindowsFakeRoot()
			opened := false
			localM3UWindowsOpenRoot = func(string) (localM3UWindowsRootHandle, error) {
				opened = true
				return fakeRoot, nil
			}
			localM3UWindowsDriveType = func(string) (uint32, error) {
				return test.driveType, nil
			}
			localM3UWindowsInspectRoot = func(localM3UWindowsRootHandle) (localM3UWindowsRootInfo, error) {
				return test.info, nil
			}
			t.Cleanup(func() {
				localM3UWindowsOpenRoot = originalOpenRoot
				localM3UWindowsDriveType = originalDriveType
				localM3UWindowsInspectRoot = originalInspectRoot
			})

			root, err := openLocalM3URoot(test.path)
			if test.want != nil {
				requireLocalM3UError(t, err, test.want)
			} else if err != nil {
				t.Fatal(err)
			}
			if root != nil {
				if err := root.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if opened != test.wantOpened {
				t.Fatalf("root opened = %v, want %v", opened, test.wantOpened)
			}
			if test.wantOpened {
				select {
				case <-fakeRoot.closed:
				default:
					t.Fatal("opened root handle was not closed")
				}
			}
		})
	}
}

func TestLocalM3UWindowsSubstitutedOpenersCancelAndCloseLateHandles(t *testing.T) {
	t.Run("root opener", func(t *testing.T) {
		originalOpenRoot := localM3UWindowsOpenRoot
		originalDriveType := localM3UWindowsDriveType
		originalInspectRoot := localM3UWindowsInspectRoot
		started := make(chan struct{})
		release := make(chan struct{})
		fakeRoot := newLocalM3UWindowsFakeRoot()
		localM3UWindowsDriveType = func(string) (uint32, error) {
			return windows.DRIVE_FIXED, nil
		}
		localM3UWindowsOpenRoot = func(string) (localM3UWindowsRootHandle, error) {
			close(started)
			<-release
			return fakeRoot, nil
		}
		localM3UWindowsInspectRoot = func(localM3UWindowsRootHandle) (localM3UWindowsRootInfo, error) {
			return localM3UWindowsRootInfo{directory: true}, nil
		}
		t.Cleanup(func() {
			localM3UWindowsOpenRoot = originalOpenRoot
			localM3UWindowsDriveType = originalDriveType
			localM3UWindowsInspectRoot = originalInspectRoot
		})

		rootPath := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			root, err := openLocalM3URootContext(ctx, rootPath)
			if root != nil {
				_ = root.Close()
			}
			result <- err
		}()
		<-started
		cancel()
		requireLocalM3UWindowsCanceledPromptly(t, result)
		close(release)
		select {
		case <-fakeRoot.closed:
		case <-time.After(time.Second):
			t.Fatal("late root handle was not closed after cancellation")
		}
	})

	t.Run("file opener", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "track.mp3")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan struct{})
		release := make(chan struct{})
		opened := make(chan *os.File, 1)
		fakeRoot := newLocalM3UWindowsFakeRoot()
		fakeRoot.lstat = func(string) (os.FileInfo, error) { return info, nil }
		fakeRoot.open = func(string) (*os.File, error) {
			close(started)
			<-release
			file, err := os.Open(path)
			opened <- file
			return file, err
		}
		root := &localM3URoot{root: fakeRoot, rootPath: filepath.Dir(path)}

		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			file, err := root.openRegularContext(ctx, filepath.Base(path), maxLocalM3UMediaBytes)
			if file != nil {
				_ = file.Close()
			}
			result <- err
		}()
		<-started
		cancel()
		requireLocalM3UWindowsCanceledPromptly(t, result)
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-fakeRoot.closed:
			t.Fatal("root handle closed while file open was pending")
		default:
		}
		close(release)
		requireLocalM3UWindowsFileClosed(t, <-opened)
		select {
		case <-fakeRoot.closed:
		case <-time.After(time.Second):
			t.Fatal("root handle remained open after pending file open completed")
		}
	})
}

func TestLocalM3UWindowsOpenedRootCannotBeRedirectedByPathReplacement(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	const safeID = "7a2adf78-b657-4596-96a4-7324d63c67d8"
	const replacementID = "3ded8f93-c020-4dd1-ae90-dae1e575a36d"
	if err := os.WriteFile(filepath.Join(rootPath, "track.mp3"), id3v24TXXX("MusicBrainz Recording Id", safeID), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := openLocalM3URoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	movedRoot := filepath.Join(parent, "moved-root")
	if err := os.Rename(rootPath, movedRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "track.mp3"), id3v24TXXX("MusicBrainz Recording Id", replacementID), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := root.openRegular("track.mp3", maxLocalM3UMediaBytes)
	if err != nil {
		// Windows may retain the root's original opened name after rename. A
		// conservative containment rejection is valid; following the replacement is not.
		requireLocalM3UError(t, err, ErrLocalM3UOutsideRoot)
		return
	}
	defer func() { _ = f.Close() }()
	got, err := musicBrainzRecordingID(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if got != safeID {
		t.Fatalf("recording ID = %q, want opened-root file %q", got, safeID)
	}
}

func TestLocalM3UWindowsRepeatedRejectionDoesNotLeakHandles(t *testing.T) {
	dir := t.TempDir()
	playlist := writeLocalM3U(t, dir, "missing.mp3\n")
	before, err := localM3UWindowsHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	for range 300 {
		_, err := NewLocalM3USource(playlist).Items(context.Background())
		requireLocalM3UError(t, err, ErrLocalM3UInvalidPath)
	}
	after, err := localM3UWindowsHandleCount()
	if err != nil {
		t.Fatal(err)
	}
	if after > before+4 {
		t.Fatalf("process handles grew from %d to %d", before, after)
	}
}

func localM3UWindowsSymlink(t *testing.T, oldName, newName string) {
	t.Helper()
	if err := os.Symlink(oldName, newName); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			t.Skipf("Windows symlink creation unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func localM3UWindowsHandleCount() (uint32, error) {
	var count uint32
	success, _, callErr := localM3UGetProcessHandleCount.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&count)),
	)
	if success == 0 {
		return 0, callErr
	}
	return count, nil
}

type localM3UWindowsFakeRoot struct {
	closed    chan struct{}
	closeOnce sync.Once
	lstat     func(string) (os.FileInfo, error)
	open      func(string) (*os.File, error)
}

func newLocalM3UWindowsFakeRoot() *localM3UWindowsFakeRoot {
	return &localM3UWindowsFakeRoot{closed: make(chan struct{})}
}

func (r *localM3UWindowsFakeRoot) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func (r *localM3UWindowsFakeRoot) Lstat(path string) (os.FileInfo, error) {
	return r.lstat(path)
}

func (r *localM3UWindowsFakeRoot) Open(path string) (*os.File, error) {
	return r.open(path)
}

func requireLocalM3UWindowsCanceledPromptly(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("cancellation did not terminate the blocked open promptly")
	}
}

func requireLocalM3UWindowsFileClosed(t *testing.T, file *os.File) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, err := file.Stat()
		if errors.Is(err, os.ErrClosed) {
			return
		}
		if err != nil {
			t.Fatalf("inspect late file handle: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("late file handle remained open after cancellation")
}
