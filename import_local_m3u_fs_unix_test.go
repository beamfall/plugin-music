//go:build unix

package music

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLocalM3UUnixRejectsPlaylistAndMediaSymlinks(t *testing.T) {
	t.Run("playlist", func(t *testing.T) {
		dir := t.TempDir()
		realPlaylist := writeLocalM3U(t, dir, "track.mp3\n")
		if err := os.WriteFile(filepath.Join(dir, "track.mp3"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		linkedPlaylist := filepath.Join(dir, "linked.m3u")
		if err := os.Symlink(filepath.Base(realPlaylist), linkedPlaylist); err != nil {
			t.Fatal(err)
		}
		_, err := NewLocalM3USource(linkedPlaylist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		requireLocalM3UError(t, err, ErrLocalM3USymlink)
	})

	t.Run("final media component", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.mp3")
		if err := os.WriteFile(outside, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "linked.mp3")); err != nil {
			t.Fatal(err)
		}
		playlist := writeLocalM3U(t, root, "linked.mp3\n")
		_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		requireLocalM3UError(t, err, ErrLocalM3USymlink)
	})

	t.Run("intermediate media component", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "outside.mp3"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
			t.Fatal(err)
		}
		playlist := writeLocalM3U(t, root, "linked/outside.mp3\n")
		_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		requireLocalM3UError(t, err, ErrLocalM3USymlink)
	})
}

func TestLocalM3UUnixRejectsFIFOAndSocketPromptly(t *testing.T) {
	t.Run("media FIFO", func(t *testing.T) {
		dir := t.TempDir()
		fifo := filepath.Join(dir, "blocked.mp3")
		if err := unix.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		playlist := writeLocalM3U(t, dir, "blocked.mp3\n")
		assertLocalM3UPromptTypedFailure(t, playlist)
	})

	t.Run("playlist FIFO", func(t *testing.T) {
		dir := t.TempDir()
		playlist := filepath.Join(dir, "blocked.m3u")
		if err := unix.Mkfifo(playlist, 0o600); err != nil {
			t.Fatal(err)
		}
		assertLocalM3UPromptTypedFailure(t, playlist)
	})

	t.Run("media socket", func(t *testing.T) {
		dir, err := os.MkdirTemp("/tmp", "m3u-socket-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		socket := filepath.Join(dir, "s")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		playlist := writeLocalM3U(t, dir, "s\n")
		assertLocalM3UPromptTypedFailure(t, playlist)
	})
}

func TestLocalM3UUnixRootClassification(t *testing.T) {
	rootPath := t.TempDir()
	tests := []struct {
		name string
		info localM3UUnixRootInfo
		want error
	}{
		{
			name: "fixed local root accepted",
			info: localM3UUnixRootInfo{mode: unix.S_IFDIR},
		},
		{
			name: "remote mount rejected",
			info: localM3UUnixRootInfo{mode: unix.S_IFDIR, remote: true},
			want: ErrLocalM3UOutsideRoot,
		},
		{
			name: "non-directory root rejected",
			info: localM3UUnixRootInfo{mode: unix.S_IFREG},
			want: ErrLocalM3UNotRegular,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := localM3UUnixInspectRoot
			localM3UUnixInspectRoot = func(int) (localM3UUnixRootInfo, error) {
				return test.info, nil
			}
			t.Cleanup(func() { localM3UUnixInspectRoot = original })

			root, err := openLocalM3URoot(rootPath)
			if test.want != nil {
				requireLocalM3UError(t, err, test.want)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := root.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLocalM3UUnixSubstitutedOpenersCancelAndCloseLateDescriptors(t *testing.T) {
	t.Run("root opener", func(t *testing.T) {
		originalOpen := localM3UUnixOpen
		originalInspect := localM3UUnixInspectRoot
		started := make(chan struct{})
		release := make(chan struct{})
		opened := make(chan int, 1)
		inspected := make(chan struct{})
		localM3UUnixOpen = func(path string, flags int) (int, error) {
			close(started)
			<-release
			fd, err := originalOpen(path, flags)
			opened <- fd
			return fd, err
		}
		localM3UUnixInspectRoot = func(int) (localM3UUnixRootInfo, error) {
			close(inspected)
			return localM3UUnixRootInfo{mode: unix.S_IFDIR}, nil
		}
		t.Cleanup(func() {
			localM3UUnixOpen = originalOpen
			localM3UUnixInspectRoot = originalInspect
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
		requireLocalM3UCanceledPromptly(t, result)
		close(release)
		<-inspected
		requireLocalM3UDescriptorClosed(t, <-opened)
	})

	t.Run("file opener", func(t *testing.T) {
		rootPath := t.TempDir()
		if err := os.WriteFile(filepath.Join(rootPath, "track.mp3"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		root, err := openLocalM3URoot(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		rootFD := int(root.dir.Fd())

		originalOpenat := localM3UUnixOpenat
		started := make(chan struct{})
		release := make(chan struct{})
		opened := make(chan int, 1)
		localM3UUnixOpenat = func(dirFD int, path string, flags int) (int, error) {
			close(started)
			<-release
			fd, err := originalOpenat(dirFD, path, flags)
			opened <- fd
			return fd, err
		}
		t.Cleanup(func() { localM3UUnixOpenat = originalOpenat })

		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			file, err := root.openRegularContext(ctx, "track.mp3", maxLocalM3UMediaBytes)
			if file != nil {
				_ = file.Close()
			}
			result <- err
		}()
		<-started
		cancel()
		requireLocalM3UCanceledPromptly(t, result)
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := unix.FcntlInt(uintptr(rootFD), unix.F_GETFD, 0); err != nil {
			t.Fatalf("root descriptor closed while file open was pending: %v", err)
		}
		close(release)
		requireLocalM3UDescriptorClosed(t, <-opened)
		requireLocalM3UDescriptorClosed(t, rootFD)
	})
}

func TestLocalM3UUnixOpenedRootSurvivesPathReplacement(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	const safeID = "65f4f0c0-ef9e-4c8a-bf89-39dca2b68079"
	const replacementID = "88a4ac78-9d33-48bd-9746-2b537077f8ad"
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
		t.Fatal(err)
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

func TestLocalM3UUnixComponentSwapCannotEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	const safeID = "a30a9762-32d0-4cd5-99c8-54f1f66bd495"
	const outsideID = "fcd06d2b-f6d4-43d5-a04d-771fda066502"
	safeDirectory := filepath.Join(root, "safe")
	slot := filepath.Join(root, "slot")
	if err := os.Mkdir(safeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(safeDirectory, "track.mp3"), id3v24TXXX("MusicBrainz Recording Id", safeID), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "track.mp3"), id3v24TXXX("MusicBrainz Recording Id", outsideID), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(safeDirectory, slot); err != nil {
		t.Fatal(err)
	}
	playlist := writeLocalM3U(t, root, "slot/track.mp3\n")

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if err := os.Rename(slot, safeDirectory); err != nil {
				done <- err
				return
			}
			if err := os.Symlink(outside, slot); err != nil {
				done <- err
				return
			}
			runtime.Gosched()
			if err := os.Remove(slot); err != nil {
				done <- err
				return
			}
			if err := os.Rename(safeDirectory, slot); err != nil {
				done <- err
				return
			}
			runtime.Gosched()
		}
	}()

	for range 500 {
		items, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		if err != nil {
			var typed *LocalM3UError
			if !errors.As(err, &typed) {
				close(stop)
				if err := <-done; err != nil {
					t.Logf("swapper stopped with: %v", err)
				}
				t.Fatalf("error type = %T, want *LocalM3UError: %v", err, err)
			}
			continue
		}
		if len(items) != 1 || len(items[0].ExternalIDs) != 1 {
			close(stop)
			if err := <-done; err != nil {
				t.Logf("swapper stopped with: %v", err)
			}
			t.Fatalf("items = %+v, want one safe item", items)
		}
		if items[0].ExternalIDs[0].Value == outsideID {
			close(stop)
			if err := <-done; err != nil {
				t.Logf("swapper stopped with: %v", err)
			}
			t.Fatal("component swap escaped the opened playlist root")
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLocalM3UUnixRepeatedRejectionDoesNotLeakDescriptors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.mp3")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "linked.mp3")); err != nil {
		t.Fatal(err)
	}
	playlist := writeLocalM3U(t, dir, "linked.mp3\n")
	before, err := localM3UOpenDescriptorCount()
	if err != nil {
		t.Skipf("descriptor count unavailable: %v", err)
	}
	for range 300 {
		_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		requireLocalM3UError(t, err, ErrLocalM3USymlink)
	}
	after, err := localM3UOpenDescriptorCount()
	if err != nil {
		t.Fatal(err)
	}
	if after > before+2 {
		t.Fatalf("open descriptors grew from %d to %d", before, after)
	}
}

func assertLocalM3UPromptTypedFailure(t *testing.T, playlist string) {
	t.Helper()
	started := time.Now()
	_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
	if time.Since(started) > time.Second {
		t.Fatalf("rejection took %s, want under 1s", time.Since(started))
	}
	var typed *LocalM3UError
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want *LocalM3UError: %v", err, err)
	}
	if !errors.Is(err, ErrLocalM3UNotRegular) && !errors.Is(err, ErrLocalM3UInvalidPath) {
		t.Fatalf("error = %v, want non-regular or invalid-path rejection", err)
	}
}

func localM3UOpenDescriptorCount() (int, error) {
	entries, err := os.ReadDir("/dev/fd")
	if err == nil {
		return len(entries), nil
	}
	entries, procErr := os.ReadDir("/proc/self/fd")
	if procErr != nil {
		return 0, err
	}
	return len(entries), nil
}

func requireLocalM3UCanceledPromptly(t *testing.T, result <-chan error) {
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

func requireLocalM3UDescriptorClosed(t *testing.T, fd int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if errors.Is(err, unix.EBADF) {
			return
		}
		if err != nil {
			t.Fatalf("inspect descriptor %d: %v", fd, err)
		}
		runtime.Gosched()
	}
	t.Fatalf("descriptor %d remained open after cancellation", fd)
}
