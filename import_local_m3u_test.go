package music

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
)

func TestLocalM3USource(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.mp3")
	second := filepath.Join(dir, "second.mp3")
	const mbid = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	if err := os.WriteFile(first, id3v24TXXX("MusicBrainz Recording Id", mbid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m3u := filepath.Join(dir, "Favourites.m3u")
	if err := os.WriteFile(m3u, []byte("#EXTM3U\nfirst.mp3\n"+second+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	source := NewLocalM3USource(m3u)
	if got := source.Categories(); len(got) != 1 || got[0] != "collections" {
		t.Fatalf("Categories() = %v, want [collections]", got)
	}
	items, err := source.Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Path != first || items[1].Path != second {
		t.Fatalf("paths = [%q %q], want [%q %q]", items[0].Path, items[1].Path, first, second)
	}
	for i, item := range items {
		if !item.CollectionMember.Present ||
			item.CollectionMember.Name != "Favourites" ||
			item.CollectionMember.Position != i {
			t.Errorf("items[%d].CollectionMember = %+v, want Favourites at %d", i, item.CollectionMember, i)
		}
	}
	if len(items[0].ExternalIDs) != 1 ||
		items[0].ExternalIDs[0].Scheme != recordingMBIDScheme ||
		items[0].ExternalIDs[0].Value != mbid {
		t.Errorf("items[0].ExternalIDs = %+v, want MusicBrainz recording ID %q", items[0].ExternalIDs, mbid)
	}
	if len(items[1].ExternalIDs) != 0 {
		t.Errorf("items[1].ExternalIDs = %+v, want none", items[1].ExternalIDs)
	}
}

func TestMusicBrainzRecordingIDOversizedTagIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.mp3")
	header := append([]byte{'I', 'D', '3', 4, 0, 0}, synchsafeBytes(maxImportBytes+1)...)
	if err := os.WriteFile(path, header, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got, err := musicBrainzRecordingID(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("musicBrainzRecordingID = %q, want empty for oversized tag", got)
	}
}

func TestMusicBrainzRecordingIDCancellationBetweenBoundedReads(t *testing.T) {
	size := localM3UReadChunkBytes + 1
	header := append([]byte{'I', 'D', '3', 4, 0, 0}, synchsafeBytes(size)...)
	contents := append(header, make([]byte, size)...)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancelOnSecondRead{
		Reader: bytes.NewReader(contents),
		cancel: cancel,
	}
	_, err := musicBrainzRecordingID(ctx, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("musicBrainzRecordingID() error = %v, want context.Canceled", err)
	}
	if reader.maxRead > localM3UReadChunkBytes {
		t.Fatalf("largest read = %d, want at most %d", reader.maxRead, localM3UReadChunkBytes)
	}
}

func TestLocalM3USourceRejectsPathsOutsidePlaylistRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "music")
	sibling := filepath.Join(parent, "music-backup")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "inside.mp3")
	outside := filepath.Join(parent, "outside.mp3")
	siblingFile := filepath.Join(sibling, "sibling.mp3")
	for _, path := range []string{inside, outside, siblingFile} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := map[string]string{
		"relative traversal":          "../outside.mp3",
		"relative internal traversal": "sub/../inside.mp3",
		"absolute traversal": root + string(filepath.Separator) + "sub" +
			string(filepath.Separator) + ".." + string(filepath.Separator) + "inside.mp3",
		"absolute outside": outside,
		"sibling prefix":   siblingFile,
	}
	for name, entry := range tests {
		t.Run(name, func(t *testing.T) {
			playlist := filepath.Join(root, strings.ReplaceAll(name, " ", "-")+".m3u")
			if err := os.WriteFile(playlist, []byte(entry+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
			requireLocalM3UError(t, err, ErrLocalM3UOutsideRoot)
		})
	}
}

func TestLocalM3URootHelperRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	root, err := openLocalM3URoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, path := range []string{
		".." + string(filepath.Separator) + "outside.mp3",
		"sub" + string(filepath.Separator) + ".." + string(filepath.Separator) + "inside.mp3",
		filepath.Join(string(filepath.Separator), "absolute.mp3"),
	} {
		_, err := root.openRegular(path, maxLocalM3UMediaBytes)
		requireLocalM3UError(t, err, ErrLocalM3UOutsideRoot)
	}
}

func TestLocalM3USourceRejectsNonRegularAndBoundedInputs(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		playlist := writeLocalM3U(t, dir, "subdir\n")
		if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		requireLocalM3UError(t, err, ErrLocalM3UNotRegular)
	})

	t.Run("sparse oversized media", func(t *testing.T) {
		dir := t.TempDir()
		media := filepath.Join(dir, "large.mp3")
		if err := os.WriteFile(media, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(media, maxLocalM3UMediaBytes+1); err != nil {
			t.Fatal(err)
		}
		playlist := writeLocalM3U(t, dir, "large.mp3\n")
		_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		requireLocalM3UError(t, err, ErrLocalM3UTooLarge)
	})

	t.Run("sparse oversized playlist", func(t *testing.T) {
		dir := t.TempDir()
		playlist := filepath.Join(dir, "large.m3u")
		if err := os.WriteFile(playlist, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(playlist, maxLocalM3UPlaylistBytes+1); err != nil {
			t.Fatal(err)
		}
		_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		requireLocalM3UError(t, err, ErrLocalM3UTooLarge)
	})

	t.Run("overlong line", func(t *testing.T) {
		dir := t.TempDir()
		playlist := writeLocalM3U(t, dir, strings.Repeat("a", maxLocalM3ULineBytes+1)+"\n")
		_, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		requireLocalM3UError(t, err, ErrLocalM3ULineTooLong)
	})

	t.Run("excessive entries", func(t *testing.T) {
		dir := t.TempDir()
		playlist := writeLocalM3U(t, dir, strings.Repeat("track.mp3\n", maxLocalM3UEntries+1))
		source := localM3USource{path: playlist}
		err := source.Stream(
			context.Background(),
			strconv.Itoa(maxLocalM3UEntries),
			func(context.Context, []sdk.ForeignItem, string) error {
				t.Fatal("emit called past maximum checkpoint")
				return nil
			},
		)
		requireLocalM3UError(t, err, ErrLocalM3UTooManyEntries)
	})
}

func TestLocalM3USourceAllowsInRootHardlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	const mbid = "d9428888-122b-11e1-b85c-61cd3cbb3210"
	original := filepath.Join(parent, "original.mp3")
	if err := os.WriteFile(original, id3v24TXXX("MusicBrainz Recording Id", mbid), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked.mp3")
	if err := os.Link(original, linked); err != nil {
		t.Fatal(err)
	}
	playlist := writeLocalM3U(t, root, "linked.mp3\n")

	items, err := NewLocalM3USource(playlist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].ExternalIDs) != 1 || items[0].ExternalIDs[0].Value != mbid {
		t.Fatalf("items = %+v, want in-root hardlink with recording ID %q", items, mbid)
	}

	oversizedOriginal := filepath.Join(parent, "oversized.mp3")
	if err := os.WriteFile(oversizedOriginal, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(oversizedOriginal, maxLocalM3UMediaBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(oversizedOriginal, filepath.Join(root, "oversized.mp3")); err != nil {
		t.Fatal(err)
	}
	oversizedPlaylist := writeLocalM3U(t, root, "oversized.mp3\n")
	_, err = NewLocalM3USource(oversizedPlaylist).Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
	requireLocalM3UError(t, err, ErrLocalM3UTooLarge)
}

func TestLocalM3USourceCancellationBetweenBoundedReads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "track.mp3"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	playlist := writeLocalM3U(t, dir, strings.Repeat("track.mp3\n", musicImportPageItems+1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emits := 0
	err := (localM3USource{path: playlist}).Stream(
		ctx,
		"",
		func(context.Context, []sdk.ForeignItem, string) error {
			emits++
			cancel()
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
	if emits != 1 {
		t.Fatalf("emit calls = %d, want 1", emits)
	}
}

func writeLocalM3U(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "playlist.m3u")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireLocalM3UError(t *testing.T, err, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %v", target)
	}
	var typed *LocalM3UError
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want *LocalM3UError: %v", err, err)
	}
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(_, %v)", err, target)
	}
}

type cancelOnSecondRead struct {
	*bytes.Reader
	cancel  context.CancelFunc
	reads   int
	maxRead int
}

func (r *cancelOnSecondRead) Read(p []byte) (int, error) {
	r.reads++
	if len(p) > r.maxRead {
		r.maxRead = len(p)
	}
	n, err := r.Reader.Read(p)
	if r.reads == 2 {
		r.cancel()
	}
	return n, err
}

func id3v24TXXX(description, value string) []byte {
	payload := append([]byte{3}, []byte(description)...)
	payload = append(payload, 0)
	payload = append(payload, value...)
	frame := append([]byte("TXXX"), synchsafeBytes(len(payload))...)
	frame = append(frame, 0, 0)
	frame = append(frame, payload...)
	tag := append([]byte{'I', 'D', '3', 4, 0, 0}, synchsafeBytes(len(frame))...)
	return append(tag, frame...)
}

func synchsafeBytes(n int) []byte {
	return []byte{byte(n >> 21), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
}
