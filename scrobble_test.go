package music

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	scrobble "github.com/beamfall/plugin-sdk/scrobble"
)

// newScrobbleTestServer starts an httptest.Server running handler and
// registers its Close via t.Cleanup, removing the repeated
// httptest.NewServer/defer Close() boilerplate from each adapter test below.
func newScrobbleTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// TestListenBrainzAdapterPostsTokenListen: the ListenBrainz adapter posts a
// single listen with the recording mbid and the "Token" auth header.
func TestListenBrainzAdapterPostsTokenListen(t *testing.T) {
	var gotAuth, gotPath, gotBody string
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})

	a := NewListenBrainzAdapter(srv.Client(), srv.URL)
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "Some Track", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-123"},
		Secret:   "user-token",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if gotAuth != "Token user-token" {
		t.Errorf("Authorization = %q, want Token user-token", gotAuth)
	}
	if gotPath != "/1/submit-listens" {
		t.Errorf("path = %q, want /1/submit-listens", gotPath)
	}
	if !strings.Contains(gotBody, "mbid-123") || !strings.Contains(gotBody, "Some Track") {
		t.Errorf("body missing identity/title: %s", gotBody)
	}
}

func TestScrobbleAdaptersDeclareConfiguredEndpointOrigins(t *testing.T) {
	lastfm := NewLastFMAdapter(nil, "https://example.test/2.0/", "api-key", "api-secret")
	if got, want := lastfm.DeclaredEndpoints(), []string{"https://example.test"}; got[0] != want[0] {
		t.Errorf("LastFMAdapter.DeclaredEndpoints() = %v, want %v", got, want)
	}

	listenbrainz := NewListenBrainzAdapter(nil, "https://listen.example.test/root/")
	if got, want := listenbrainz.DeclaredEndpoints(), []string{"https://listen.example.test"}; got[0] != want[0] {
		t.Errorf("ListenBrainzAdapter.DeclaredEndpoints() = %v, want %v", got, want)
	}
}

func TestScrobbleAdaptersDeclareDefaultEndpointOrigins(t *testing.T) {
	lastfm := NewLastFMAdapter(nil, "", "api-key", "api-secret")
	if got, want := lastfm.DeclaredEndpoints(), []string{"https://ws.audioscrobbler.com"}; got[0] != want[0] {
		t.Errorf("LastFMAdapter.DeclaredEndpoints() = %v, want %v", got, want)
	}

	listenbrainz := NewListenBrainzAdapter(nil, "")
	if got, want := listenbrainz.DeclaredEndpoints(), []string{"https://api.listenbrainz.org"}; got[0] != want[0] {
		t.Errorf("ListenBrainzAdapter.DeclaredEndpoints() = %v, want %v", got, want)
	}
}

func TestScrobbleAdaptersDoNotSelfProvisionNilClients(t *testing.T) {
	if got := NewLastFMAdapter(nil, "", "api-key", "api-secret").client; got != nil {
		t.Errorf("LastFMAdapter.client = %#v, want nil", got)
	}
	if got := NewListenBrainzAdapter(nil, "").client; got != nil {
		t.Errorf("ListenBrainzAdapter.client = %#v, want nil", got)
	}
}

// TestLastFMAdapterSignsForm: the Last.fm adapter posts a signed form whose
// api_sig matches the documented md5(sorted params + secret) scheme and whose
// session key rides sk.
func TestLastFMAdapterSignsForm(t *testing.T) {
	var form string
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form = string(b)
		w.WriteHeader(http.StatusOK)
	})

	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !strings.Contains(form, "sk=session-key-7") {
		t.Errorf("form missing session key: %s", form)
	}
	if !strings.Contains(form, "api_sig=") {
		t.Errorf("form missing api_sig: %s", form)
	}
	if !strings.Contains(form, "method=track.scrobble") {
		t.Errorf("form missing method: %s", form)
	}
}

// TestLastFMAdapterEnrichesArtistAlbumWhenPresent —
// when the watched event carries hydrated artist/album, the Last.fm scrobble
// includes them as form params, AND the api_sig covers them (a signature that
// omitted artist would be rejected by Last.fm).
func TestLastFMAdapterEnrichesArtistAlbumWhenPresent(t *testing.T) {
	var form string
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form = string(b)
		w.WriteHeader(http.StatusOK)
	})

	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event: scrobble.WatchedEvent{
			Title:     "A Song",
			Music:     scrobble.MusicMetadata{Artist: "An Artist", Album: "An Album"},
			WatchedAt: time.Unix(1_700_000_000, 0),
		},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !strings.Contains(form, "artist=An+Artist") {
		t.Errorf("form missing enriched artist: %s", form)
	}
	if !strings.Contains(form, "album=An+Album") {
		t.Errorf("form missing enriched album: %s", form)
	}
	// The signature must cover artist+album: recomputing with them present must
	// equal the api_sig the adapter sent.
	values, err := url.ParseQuery(form)
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	signed := map[string]string{
		"method":    "track.scrobble",
		"api_key":   "api-key-1",
		"sk":        "session-key-7",
		"track":     "A Song",
		"mbid":      "mbid-9",
		"artist":    "An Artist",
		"album":     "An Album",
		"timestamp": values.Get("timestamp"),
	}
	if want := lastfmSignature(signed, "shared-secret-1"); values.Get("api_sig") != want {
		t.Errorf("api_sig does not cover artist/album: got %q want %q", values.Get("api_sig"), want)
	}
}

// TestLastFMAdapterOmitsArtistAlbumWhenAbsent: degrade path — an
// un-hydrated track (empty MusicMetadata) sends no artist/album params; the
// recording mbid remains the identity handle.
func TestLastFMAdapterOmitsArtistAlbumWhenAbsent(t *testing.T) {
	var form string
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form = string(b)
		w.WriteHeader(http.StatusOK)
	})

	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	if err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if strings.Contains(form, "artist=") || strings.Contains(form, "album=") {
		t.Errorf("degraded scrobble must omit empty artist/album: %s", form)
	}
	if !strings.Contains(form, "mbid=mbid-9") {
		t.Errorf("recording mbid must remain the identity handle: %s", form)
	}
}

// TestListenBrainzAdapterEnrichesMetadataWhenPresent — a hydrated audio
// event carries artist_name/release_name + artist_mbids/release_mbid in the
// ListenBrainz submission; an absent field is omitted.
func TestListenBrainzAdapterEnrichesMetadataWhenPresent(t *testing.T) {
	var body string
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	})

	a := NewListenBrainzAdapter(srv.Client(), srv.URL)
	if err := a.Submit(context.Background(), scrobble.Scrobble{
		Event: scrobble.WatchedEvent{
			Title: "Some Track",
			Music: scrobble.MusicMetadata{
				Artist: "An Artist", Album: "An Album",
				ArtistMBID: "artist-mbid-1", ReleaseMBID: "release-mbid-2",
			},
			WatchedAt: time.Unix(1_700_000_000, 0),
		},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-123"},
		Secret:   "user-token",
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	for _, want := range []string{"An Artist", "An Album", "artist_name", "release_name", "artist-mbid-1", "release-mbid-2", "artist_mbids", "release_mbid"} {
		if !strings.Contains(body, want) {
			t.Errorf("ListenBrainz body missing %q: %s", want, body)
		}
	}
}

// TestListenBrainzAdapterDegradesWhenMetadataAbsent: an un-hydrated listen omits
// artist_name/release_name and the optional mbids, keeping only recording_mbid.
func TestListenBrainzAdapterDegradesWhenMetadataAbsent(t *testing.T) {
	var body string
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	})

	a := NewListenBrainzAdapter(srv.Client(), srv.URL)
	if err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "Some Track", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-123"},
		Secret:   "user-token",
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	for _, absent := range []string{"artist_name", "release_name", "artist_mbids", "release_mbid"} {
		if strings.Contains(body, absent) {
			t.Errorf("degraded listen must omit %q: %s", absent, body)
		}
	}
	if !strings.Contains(body, "recording_mbid") {
		t.Errorf("recording_mbid must remain: %s", body)
	}
}

// TestLastFMAdapterNon2xxResponseReturnsError: the Last.fm adapter surfaces a
// non-2xx endpoint response as an error rather than silently treating the
// scrobble as delivered.
func TestLastFMAdapterNon2xxResponseReturnsError(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	})
	if err == nil {
		t.Fatal("Submit err = nil, want an error for a 403 response")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("Submit err = %q, want it to mention the 403 status", err.Error())
	}
}

// TestLastFMAdapterErrorEnvelopeOn2xxReturnsError: Last.fm can embed a failed
// submission (documented error codes 1-29) in a 200 OK body instead of a
// non-2xx status. Submit must surface that as an error rather than treating
// the scrobble as delivered, and the error must not echo the response body,
// the session key, or the submitted track title.
func TestLastFMAdapterErrorEnvelopeOn2xxReturnsError(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":9,"message":"Invalid session key - session-key-7"}`))
	})

	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	})
	if err == nil {
		t.Fatal("Submit err = nil, want an error for a 2xx error envelope")
	}
	if !strings.Contains(err.Error(), "9") {
		t.Errorf("Submit err = %q, want it to mention the error code 9", err.Error())
	}
	if strings.Contains(err.Error(), "session-key-7") {
		t.Errorf("Submit err = %q, must not echo the session key", err.Error())
	}
	if strings.Contains(err.Error(), "Invalid session key") {
		t.Errorf("Submit err = %q, must not echo the response body message", err.Error())
	}
}

// TestLastFMAdapterAcceptsSuccessBody: a 2xx body carrying Last.fm's
// documented success envelope (no "error" key) is accepted, not misread as a
// failure.
func TestLastFMAdapterAcceptsSuccessBody(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"scrobbles":{"@attr":{"accepted":1,"ignored":0}}}`))
	})

	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	if err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	}); err != nil {
		t.Fatalf("Submit err = %v, want nil for a documented success body", err)
	}
}

// TestLastFMAdapterAcceptsEmptyBody: a 2xx response with no body at all (the
// other documented success shape) is accepted, matching TestLastFMAdapterSignsForm.
func TestLastFMAdapterAcceptsEmptyBody(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	if err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	}); err != nil {
		t.Fatalf("Submit err = %v, want nil for an empty success body", err)
	}
}

// TestLastFMAdapterUnverifiable2xxBodyFailsClosed: a 2xx body that cannot be
// positively verified as the documented empty/success shape — malformed JSON,
// a non-object shape, or a body larger than the bounded read
// (lastfmEnvelopeBodyLimit) — must be treated as a FAILED submission, not a
// silent success: an unverifiable body could be hiding the documented error
// envelope lastfmEnvelopeError exists to catch. The oversized case also
// exercises the io.LimitReader+1 truncation check: the handler writes well
// past the limit and Submit must still return an error rather than swallowing
// the excess bytes as "no envelope found".
func TestLastFMAdapterUnverifiable2xxBodyFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"malformed json", []byte(`{"error":`)},
		{"non-envelope shape", []byte(`["not","an","object"]`)},
		{"scalar string", []byte(`"not-an-object"`)},
		{"scalar number", []byte(`7`)},
		{"scalar boolean", []byte(`true`)},
		{"oversized non-envelope", []byte(strings.Repeat("x", (1<<16)+4096))},
		{"oversized past limit, benign looking", []byte(`{"error":0,` + strings.Repeat(`"x":"y",`, 1<<13) + `"z":1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tc.body)
			})
			a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
			err := a.Submit(context.Background(), scrobble.Scrobble{
				Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
				Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
				Secret:   "session-key-7",
			})
			if err == nil {
				t.Fatalf("Submit err = nil, want non-nil (fail-closed) for a %s 2xx body: an unverifiable body must not be counted as delivered", tc.name)
			}
		})
	}
}

// TestLastFMAdapterUnreadableBodyFailsClosed: if reading the 2xx response body
// itself errors (e.g. the connection drops mid-body), Submit must return that
// error rather than nil — a body we couldn't even read cannot be verified as
// the documented success shape.
func TestLastFMAdapterUnreadableBodyFailsClosed(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"err`)) // declared length not honored, body cut short
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close() // force a read error on the client side
			}
		}
	})
	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	})
	if err == nil {
		t.Fatalf("Submit err = nil, want non-nil (fail-closed) for an unreadable/truncated response body")
	}
}

// TestLastFMAdapterNullBodyFailsClosed: a 2xx body that is the JSON literal
// `null` decodes into the zero envelope (error==0) without a parse error, so it
// must be rejected explicitly — otherwise it would masquerade as the documented
// success body and count an unverified submission as delivered.
func TestLastFMAdapterNullBodyFailsClosed(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("null"))
	})
	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	})
	if err == nil {
		t.Fatal("Submit err = nil, want non-nil (fail-closed) for a JSON null 2xx body")
	}
}

// failingBody is an io.ReadCloser whose Read always fails with a fixed error
// carrying a sentinel string, used to prove lastfmEnvelopeError does not echo
// the raw reader error — a hostile body reader could stuff unbounded or
// sensitive text into that error value.
type failingBody struct{ err error }

func (f failingBody) Read([]byte) (int, error) { return 0, f.err }
func (f failingBody) Close() error             { return nil }

// TestLastFMEnvelopeErrorReadErrorIsBoundedAndDoesNotLeak: when reading the 2xx
// body errors, lastfmEnvelopeError must fail closed with a bounded, static
// message and must never wrap/echo the raw reader error text.
func TestLastFMEnvelopeErrorReadErrorIsBoundedAndDoesNotLeak(t *testing.T) {
	const sensitive = "SENSITIVE-session-key-7-plus-a-very-long-injected-blob-0xdeadbeef"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       failingBody{err: errors.New(sensitive)},
	}
	err := lastfmEnvelopeError(resp)
	if err == nil {
		t.Fatal("lastfmEnvelopeError = nil, want non-nil (fail-closed) when the body read errors")
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Errorf("err = %q, must not echo the raw reader error text %q", err.Error(), sensitive)
	}
}

// TestLastFMEnvelopeErrorRejectsUnverifiableErrorValuesWithoutLeak proves the
// error member itself must be a verifiable integer. In particular, JSON null
// must not become int's zero-value success, and decoder failures/overflow must
// not echo attacker-controlled literals through wrapped errors.
func TestLastFMEnvelopeErrorRejectsUnverifiableErrorValuesWithoutLeak(t *testing.T) {
	const marker = "INJECTED-sensitive-error-value"
	overflow := strings.Repeat("9", 4096)
	cases := []struct {
		name      string
		body      string
		forbidden string
	}{
		{name: "null", body: `{"error":null}`},
		{name: "string", body: `{"error":"` + marker + `"}`, forbidden: marker},
		{name: "object", body: `{"error":{"value":"` + marker + `"}}`, forbidden: marker},
		{name: "overflow", body: `{"error":` + overflow + `}`, forbidden: overflow[:32]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}
			err := lastfmEnvelopeError(resp)
			if err == nil {
				t.Fatal("lastfmEnvelopeError err = nil, want fail-closed error")
			}
			if len(err.Error()) > 128 {
				t.Errorf("error length = %d, want bounded to 128 bytes", len(err.Error()))
			}
			if tc.forbidden != "" && strings.Contains(err.Error(), tc.forbidden) {
				t.Errorf("error leaked response-derived value %q", tc.forbidden)
			}
		})
	}
}

// TestListenBrainzAdapterNon2xxResponseReturnsError: the ListenBrainz adapter
// surfaces a non-2xx endpoint response as an error rather than silently
// treating the listen as delivered.
func TestListenBrainzAdapterNon2xxResponseReturnsError(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	a := NewListenBrainzAdapter(srv.Client(), srv.URL)
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "Some Track", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-123"},
		Secret:   "user-token",
	})
	if err == nil {
		t.Fatal("Submit err = nil, want an error for a 429 response")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("Submit err = %q, want it to mention the 429 status", err.Error())
	}
}

// TestLastFMAdapterRateLimitedReturnsTypedError: a 429 from Last.fm returns a
// *RateLimitedError matching ErrRateLimited (not just a generic status-code
// error), with the Retry-After header parsed into RetryAfter.
func TestLastFMAdapterRateLimitedReturnsTypedError(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	a := NewLastFMAdapter(srv.Client(), srv.URL, "api-key-1", "shared-secret-1")
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "A Song", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-9"},
		Secret:   "session-key-7",
	})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Submit err = %v, want errors.Is(err, ErrRateLimited)", err)
	}
	var rle *RateLimitedError
	if !errors.As(err, &rle) {
		t.Fatalf("Submit err = %v, want errors.As match for *RateLimitedError", err)
	}
	if rle.Service != ServiceLastFM {
		t.Errorf("RateLimitedError.Service = %q, want %q", rle.Service, ServiceLastFM)
	}
	if rle.RetryAfter != 30*time.Second {
		t.Errorf("RateLimitedError.RetryAfter = %s, want 30s", rle.RetryAfter)
	}
}

// TestListenBrainzAdapterRateLimitedReturnsTypedError: a 429 from ListenBrainz
// returns a *RateLimitedError matching ErrRateLimited; without a Retry-After
// header, RetryAfter is zero rather than a guessed value.
func TestListenBrainzAdapterRateLimitedReturnsTypedError(t *testing.T) {
	srv := newScrobbleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	a := NewListenBrainzAdapter(srv.Client(), srv.URL)
	err := a.Submit(context.Background(), scrobble.Scrobble{
		Event:    scrobble.WatchedEvent{Title: "Some Track", WatchedAt: time.Unix(1_700_000_000, 0)},
		Identity: scrobble.ExternalID{Scheme: SchemeMusicBrainzRecording, Value: "mbid-123"},
		Secret:   "user-token",
	})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Submit err = %v, want errors.Is(err, ErrRateLimited)", err)
	}
	var rle *RateLimitedError
	if !errors.As(err, &rle) {
		t.Fatalf("Submit err = %v, want errors.As match for *RateLimitedError", err)
	}
	if rle.Service != ServiceListenBrainz {
		t.Errorf("RateLimitedError.Service = %q, want %q", rle.Service, ServiceListenBrainz)
	}
	if rle.RetryAfter != 0 {
		t.Errorf("RateLimitedError.RetryAfter = %s, want 0 (no header sent)", rle.RetryAfter)
	}
}

// TestRetryAfterDurationParsesSecondsAndHTTPDate: retryAfterDuration accepts
// both Retry-After forms per RFC 9110 §10.2.3 (delay-seconds and HTTP-date),
// and returns zero for absent/unparseable/negative/past values.
func TestRetryAfterDurationParsesSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	h := http.Header{}
	h.Set("Retry-After", "120")
	if got := retryAfterDuration(h, now); got != 120*time.Second {
		t.Errorf("seconds form: got %s, want 120s", got)
	}

	h = http.Header{}
	h.Set("Retry-After", now.Add(90*time.Second).Format(http.TimeFormat))
	if got := retryAfterDuration(h, now); got != 90*time.Second {
		t.Errorf("http-date form: got %s, want ~90s", got)
	}

	h = http.Header{}
	if got := retryAfterDuration(h, now); got != 0 {
		t.Errorf("absent header: got %s, want 0", got)
	}

	h = http.Header{}
	h.Set("Retry-After", "not-a-number")
	if got := retryAfterDuration(h, now); got != 0 {
		t.Errorf("unparseable: got %s, want 0", got)
	}

	h = http.Header{}
	h.Set("Retry-After", "-5")
	if got := retryAfterDuration(h, now); got != 0 {
		t.Errorf("negative seconds: got %s, want 0", got)
	}

	h = http.Header{}
	h.Set("Retry-After", now.Add(-time.Hour).Format(http.TimeFormat))
	if got := retryAfterDuration(h, now); got != 0 {
		t.Errorf("past http-date: got %s, want 0", got)
	}
}

// TestLastFMSignatureGoldenVector: pins lastfmSignature to a precomputed MD5
// digest for a fixed set of params + secret, per Last.fm's documented signing
// algorithm (sorted param keys concatenated as key+value, secret appended,
// MD5, lowercase hex). Guards against accidental changes to sort order,
// concatenation, or secret placement — any of those would change the digest.
func TestLastFMSignatureGoldenVector(t *testing.T) {
	params := map[string]string{
		"api_key": "ak",
		"method":  "auth.getSession",
		"sk":      "sess",
		"track":   "Song",
	}
	const wantDigest = "2aa3f2b9a28f4a5cfab52229352d0fe7"
	if got := lastfmSignature(params, "secret"); got != wantDigest {
		t.Errorf("lastfmSignature golden vector = %q, want %q", got, wantDigest)
	}
}

// TestLastFMSignatureExcludesFormatAndIsStable: the signing helper excludes
// format/api_sig and is deterministic for a fixed input.
func TestLastFMSignatureExcludesFormatAndIsStable(t *testing.T) {
	params := map[string]string{
		"method":  "track.scrobble",
		"api_key": "k",
		"track":   "t",
		"format":  "json", // must be excluded
	}
	got1 := lastfmSignature(params, "secret")
	got2 := lastfmSignature(params, "secret")
	if got1 != got2 {
		t.Fatal("signature must be deterministic")
	}
	// Removing the excluded format key must not change the signature.
	delete(params, "format")
	if got3 := lastfmSignature(params, "secret"); got3 != got1 {
		t.Errorf("format must be excluded from the signature (%q != %q)", got3, got1)
	}
}

// fakeSecretSource is a scrobble.SecretSource test double returning a fixed
// value (possibly empty) for every ref with no error, simulating a
// misconfigured or stub secret store that resolves a ref to blank content
// rather than failing outright.
type fakeSecretSource struct{ value string }

func (f fakeSecretSource) Secret(string) (string, error) { return f.value, nil }

// TestLastFMScrobbleFactoryRejectsEmptyAPIKey: EC-4 — when the SecretSource
// resolves the api_key_ref to an empty string without an error, the factory
// must return a clear error rather than silently building an adapter that
// signs every request with a blank api_key.
func TestLastFMScrobbleFactoryRejectsEmptyAPIKey(t *testing.T) {
	cfg := scrobble.AdapterConfig{APIKeyRef: "key-ref", APISecretRef: "secret-ref"}
	_, err := LastFMScrobbleFactory(http.DefaultClient, cfg, fakeSecretSource{value: ""})
	if err == nil {
		t.Fatal("LastFMScrobbleFactory err = nil, want error for empty api key")
	}
	if !strings.Contains(err.Error(), "api key") {
		t.Errorf("err = %q, want it to mention the api key", err)
	}
}

// TestLastFMScrobbleFactoryRejectsEmptyAPISecret: same gap as above for the
// api_secret_ref once the api key resolves non-empty.
func TestLastFMScrobbleFactoryRejectsEmptyAPISecret(t *testing.T) {
	cfg := scrobble.AdapterConfig{APIKeyRef: "key-ref", APISecretRef: "secret-ref"}
	secrets := sequentialSecretSource{values: []string{"real-api-key", ""}}
	_, err := LastFMScrobbleFactory(http.DefaultClient, cfg, &secrets)
	if err == nil {
		t.Fatal("LastFMScrobbleFactory err = nil, want error for empty api secret")
	}
	if !strings.Contains(err.Error(), "api secret") {
		t.Errorf("err = %q, want it to mention the api secret", err)
	}
}

// sequentialSecretSource returns successive values from values on each call,
// letting a test give the api_key_ref and api_secret_ref resolutions distinct
// results (e.g. a valid key followed by an empty secret).
type sequentialSecretSource struct {
	values []string
	calls  int
}

func (s *sequentialSecretSource) Secret(string) (string, error) {
	v := s.values[s.calls]
	s.calls++
	return v, nil
}

// TestLastFMScrobbleFactoryAcceptsNonEmptyCredentials: the non-empty-credential
// validation must not reject a well-formed config — a factory call with
// genuine key/secret values still succeeds.
func TestLastFMScrobbleFactoryAcceptsNonEmptyCredentials(t *testing.T) {
	cfg := scrobble.AdapterConfig{APIKeyRef: "key-ref", APISecretRef: "secret-ref", BaseURL: "https://example.test"}
	secrets := sequentialSecretSource{values: []string{"real-api-key", "real-api-secret"}}
	adapter, err := LastFMScrobbleFactory(http.DefaultClient, cfg, &secrets)
	if err != nil {
		t.Fatalf("LastFMScrobbleFactory err = %v, want nil", err)
	}
	if adapter == nil {
		t.Fatal("LastFMScrobbleFactory adapter = nil, want a non-nil adapter")
	}
}
