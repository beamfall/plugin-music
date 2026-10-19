package music

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	sdk "github.com/beamfall/plugin-sdk"
)

const LocalM3UImporterID = "local-m3u"

const (
	maxLocalM3UPlaylistBytes = maxImportBytes
	maxLocalM3UMediaBytes    = maxImportBytes
	maxLocalM3ULineBytes     = 64 << 10
	maxLocalM3UEntries       = 100_000
	localM3UReadChunkBytes   = 32 << 10
)

var (
	// ErrLocalM3UInvalidPath identifies a malformed, missing, or inaccessible path.
	ErrLocalM3UInvalidPath = errors.New("invalid local M3U path")
	// ErrLocalM3UOutsideRoot identifies a playlist entry outside the selected playlist directory.
	ErrLocalM3UOutsideRoot = errors.New("local M3U path is outside the playlist root")
	// ErrLocalM3USymlink identifies a symbolic link or Windows reparse point in an opened path.
	ErrLocalM3USymlink = errors.New("local M3U path contains a link")
	// ErrLocalM3UNotRegular identifies a FIFO, device, socket, directory, or other non-regular node.
	ErrLocalM3UNotRegular = errors.New("local M3U path is not a regular file")
	// ErrLocalM3UTooLarge identifies a playlist or media file over its byte limit.
	ErrLocalM3UTooLarge = errors.New("local M3U file exceeds the size limit")
	// ErrLocalM3ULineTooLong identifies a playlist line over its byte limit.
	ErrLocalM3ULineTooLong = errors.New("local M3U line exceeds the length limit")
	// ErrLocalM3UTooManyEntries identifies a playlist over its entry limit.
	ErrLocalM3UTooManyEntries = errors.New("local M3U playlist exceeds the entry limit")
)

// LocalM3UError reports a bounded local-playlist filesystem failure.
type LocalM3UError struct {
	Op   string
	Path string
	Err  error
}

func (e *LocalM3UError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("local M3U %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("local M3U %s %q: %v", e.Op, e.Path, e.Err)
}

// Unwrap exposes both the stable error class and its platform cause.
func (e *LocalM3UError) Unwrap() error { return e.Err }

func newLocalM3UError(op, path string, kind, cause error) error {
	if cause != nil {
		kind = fmt.Errorf("%w: %w", kind, cause)
	}
	return &LocalM3UError{Op: op, Path: path, Err: kind}
}

type localM3USource struct {
	path string
}

type localM3UOpenResult[T interface{ Close() error }] struct {
	handle T
	err    error
}

type localM3URootLifetime struct {
	mu      sync.Mutex
	users   int
	closing bool
	closed  bool
}

func (l *localM3URootLifetime) acquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return false
	}
	l.users++
	return true
}

func (l *localM3URootLifetime) release(closeHandle func() error) {
	l.mu.Lock()
	l.users--
	closeNow := l.closing && l.users == 0 && !l.closed
	if closeNow {
		l.closed = true
	}
	l.mu.Unlock()
	if closeNow {
		_ = closeHandle()
	}
}

func (l *localM3URootLifetime) close(closeHandle func() error) error {
	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		return nil
	}
	l.closing = true
	if l.users > 0 {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	return closeHandle()
}

func localM3UOpenWithContext[T interface{ Close() error }](
	ctx context.Context,
	open func() (T, error),
) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	result := make(chan localM3UOpenResult[T])
	go func() {
		handle, err := open()
		select {
		case result <- localM3UOpenResult[T]{handle: handle, err: err}:
		case <-ctx.Done():
			if err == nil {
				_ = handle.Close()
			}
		}
	}()

	select {
	case opened := <-result:
		if err := ctx.Err(); err != nil {
			if opened.err == nil {
				_ = opened.handle.Close()
			}
			return zero, err
		}
		return opened.handle, opened.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func openLocalM3URootContext(ctx context.Context, path string) (*localM3URoot, error) {
	return localM3UOpenWithContext(ctx, func() (*localM3URoot, error) {
		return openLocalM3URoot(path)
	})
}

func (r *localM3URoot) openRegularContext(
	ctx context.Context,
	relativePath string,
	maxBytes int64,
) (*os.File, error) {
	return localM3UOpenWithContext(ctx, func() (*os.File, error) {
		if !r.lifetime.acquire() {
			path := filepath.Join(r.path(), relativePath)
			return nil, newLocalM3UError("open", path, ErrLocalM3UInvalidPath, os.ErrClosed)
		}
		defer r.lifetime.release(r.closeHandle)
		return r.openRegular(relativePath, maxBytes)
	})
}

func (localM3USource) Categories() []string { return []string{sdk.CategoryCollections} }

func (s localM3USource) Items(ctx context.Context) ([]sdk.ForeignItem, error) {
	var items []sdk.ForeignItem
	err := s.Stream(ctx, "", func(_ context.Context, page []sdk.ForeignItem, _ string) error {
		items = append(items, page...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (s localM3USource) Stream(
	ctx context.Context,
	checkpoint string,
	emit func(context.Context, []sdk.ForeignItem, string) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, f, playlistPath, err := openLocalM3UPlaylist(ctx, s.path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	defer func() { _ = f.Close() }()

	resume, err := musicImportCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(playlistPath), filepath.Ext(playlistPath))
	position := 0
	page := make([]sdk.ForeignItem, 0, musicImportPageItems)
	reader := &localM3UContextReader{ctx: ctx, reader: f}
	limited := &io.LimitedReader{R: reader, N: maxLocalM3UPlaylistBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxLocalM3ULineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if path == "" || strings.HasPrefix(path, "#") {
			continue
		}
		if position >= maxLocalM3UEntries {
			return newLocalM3UError("scan", playlistPath, ErrLocalM3UTooManyEntries, nil)
		}
		if position < resume {
			position++
			continue
		}
		relativePath, itemPath, err := localM3UMediaPath(root.path(), path)
		if err != nil {
			return err
		}
		media, err := root.openRegularContext(ctx, relativePath, maxLocalM3UMediaBytes)
		if err != nil {
			return err
		}
		id, idErr := musicBrainzRecordingID(ctx, media)
		closeErr := media.Close()
		if idErr != nil {
			return idErr
		}
		if closeErr != nil {
			return newLocalM3UError("close", itemPath, ErrLocalM3UInvalidPath, closeErr)
		}
		item := sdk.ForeignItem{
			Path: itemPath,
			CollectionMember: sdk.CollectionMember{
				Present:  true,
				Name:     name,
				Position: position,
			},
		}
		if id != "" {
			item.ExternalIDs = []sdk.ExternalID{{Scheme: recordingMBIDScheme, Value: id}}
		}
		page = append(page, item)
		position++
		if len(page) < musicImportPageItems {
			continue
		}
		if err := emit(ctx, page, strconv.Itoa(position)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		page = make([]sdk.ForeignItem, 0, musicImportPageItems)
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, bufio.ErrTooLong) {
			return newLocalM3UError("scan", playlistPath, ErrLocalM3ULineTooLong, err)
		}
		return newLocalM3UError("read", playlistPath, ErrLocalM3UInvalidPath, err)
	}
	if limited.N == 0 {
		return newLocalM3UError("read", playlistPath, ErrLocalM3UTooLarge, nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if position < resume {
		return fmt.Errorf("music import checkpoint %q is beyond the source", checkpoint)
	}
	if len(page) > 0 {
		if err := emit(ctx, page, strconv.Itoa(position)); err != nil {
			return err
		}
		return ctx.Err()
	}
	return nil
}

func NewLocalM3USource(path string) sdk.Source { //nolint:staticcheck // deliberate SDK 0.x compatibility path for pre-streaming consumers
	return localM3USource{path: path}
}

func openLocalM3UPlaylist(ctx context.Context, path string) (*localM3URoot, *os.File, string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, "", newLocalM3UError("resolve", path, ErrLocalM3UInvalidPath, err)
	}
	absPath = filepath.Clean(absPath)
	// The opened parent, not a string prefix, is the authority for every
	// playlist entry resolved below it.
	root, err := openLocalM3URootContext(ctx, filepath.Dir(absPath))
	if err != nil {
		return nil, nil, "", err
	}
	f, err := root.openRegularContext(ctx, filepath.Base(absPath), maxLocalM3UPlaylistBytes)
	if err != nil {
		_ = root.Close()
		return nil, nil, "", err
	}
	return root, f, absPath, nil
}

func localM3UMediaPath(rootPath, entry string) (string, string, error) {
	nativePath := filepath.FromSlash(entry)
	if localM3UHasTraversal(nativePath) {
		return "", "", newLocalM3UError("resolve", entry, ErrLocalM3UOutsideRoot, nil)
	}
	cleanPath := filepath.Clean(nativePath)
	if cleanPath == "." || cleanPath == string(filepath.Separator) {
		return "", "", newLocalM3UError("resolve", entry, ErrLocalM3UInvalidPath, nil)
	}

	if !filepath.IsAbs(cleanPath) {
		if filepath.VolumeName(cleanPath) != "" {
			return "", "", newLocalM3UError("resolve", entry, ErrLocalM3UOutsideRoot, nil)
		}
		if err := validateLocalM3URelativePath(cleanPath); err != nil {
			return "", "", err
		}
		return cleanPath, filepath.Join(rootPath, cleanPath), nil
	}

	relativePath, err := filepath.Rel(rootPath, cleanPath)
	if err != nil {
		return "", "", newLocalM3UError("resolve", entry, ErrLocalM3UOutsideRoot, err)
	}
	if !localM3UContainedRelativePath(relativePath) {
		return "", "", newLocalM3UError("resolve", entry, ErrLocalM3UOutsideRoot, nil)
	}
	return relativePath, filepath.Join(rootPath, relativePath), nil
}

func localM3UHasTraversal(path string) bool {
	for _, component := range strings.FieldsFunc(path, func(r rune) bool {
		return r == rune(filepath.Separator)
	}) {
		if component == ".." {
			return true
		}
	}
	return false
}

func localM3UContainedRelativePath(path string) bool {
	if path == "." || !filepath.IsLocal(path) || filepath.VolumeName(path) != "" {
		return false
	}
	return path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func validateLocalM3URelativePath(path string) error {
	if path == "" || filepath.Clean(path) != path {
		return newLocalM3UError("resolve", path, ErrLocalM3UOutsideRoot, nil)
	}
	if localM3UHasTraversal(path) || !localM3UContainedRelativePath(path) {
		return newLocalM3UError("resolve", path, ErrLocalM3UOutsideRoot, nil)
	}
	return nil
}

type localM3UContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *localM3UContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > localM3UReadChunkBytes {
		p = p[:localM3UReadChunkBytes]
	}
	return r.reader.Read(p)
}

func musicBrainzRecordingID(ctx context.Context, source io.Reader) (string, error) {
	reader := &localM3UContextReader{ctx: ctx, reader: source}

	header := make([]byte, 10)
	if _, err := io.ReadFull(reader, header); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", nil
	}
	if string(header[:3]) != "ID3" {
		return "", nil
	}
	version := header[3]
	if version != 3 && version != 4 {
		return "", nil
	}
	size, ok := synchsafe(header[6:10])
	if !ok {
		return "", nil
	}
	if size > maxLocalM3UMediaBytes {
		return "", nil
	}
	tag := make([]byte, size)
	if _, err := io.ReadFull(reader, tag); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", nil
	}
	for len(tag) >= 10 && tag[0] != 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		frameSize := int(binary.BigEndian.Uint32(tag[4:8]))
		if version == 4 {
			var valid bool
			frameSize, valid = synchsafe(tag[4:8])
			if !valid {
				return "", nil
			}
		}
		if frameSize <= 0 || frameSize > len(tag)-10 {
			return "", nil
		}
		if string(tag[:4]) == "TXXX" {
			description, value := splitUserText(tag[10 : 10+frameSize])
			key := strings.ToLower(strings.ReplaceAll(description, "_", " "))
			if strings.Contains(key, "musicbrainz") &&
				(strings.Contains(key, "recording") || strings.Contains(key, "track")) {
				return strings.TrimSpace(value), nil
			}
		}
		tag = tag[10+frameSize:]
	}
	return "", nil
}

func synchsafe(b []byte) (int, bool) {
	if len(b) != 4 || b[0]&0x80 != 0 || b[1]&0x80 != 0 || b[2]&0x80 != 0 || b[3]&0x80 != 0 {
		return 0, false
	}
	return int(b[0])<<21 | int(b[1])<<14 | int(b[2])<<7 | int(b[3]), true
}

func splitUserText(frame []byte) (string, string) {
	if len(frame) < 2 {
		return "", ""
	}
	encoding := frame[0]
	frame = frame[1:]
	switch encoding {
	case 0, 3:
		if i := strings.IndexByte(string(frame), 0); i >= 0 {
			return decodeText(encoding, frame[:i]), decodeText(encoding, frame[i+1:])
		}
	case 1, 2:
		for i := 0; i+1 < len(frame); i += 2 {
			if frame[i] == 0 && frame[i+1] == 0 {
				return decodeText(encoding, frame[:i]), decodeText(encoding, frame[i+2:])
			}
		}
	}
	return "", ""
}

func decodeText(encoding byte, b []byte) string {
	if encoding == 0 || encoding == 3 {
		return strings.TrimRight(string(b), "\x00")
	}
	littleEndian := false
	if encoding == 1 && len(b) >= 2 {
		littleEndian = b[0] == 0xff && b[1] == 0xfe
		if littleEndian || b[0] == 0xfe && b[1] == 0xff {
			b = b[2:]
		}
	}
	u16 := make([]uint16, 0, len(b)/2)
	for len(b) >= 2 {
		if littleEndian {
			u16 = append(u16, binary.LittleEndian.Uint16(b))
		} else {
			u16 = append(u16, binary.BigEndian.Uint16(b))
		}
		b = b[2:]
	}
	return strings.TrimRight(string(utf16.Decode(u16)), "\x00")
}
