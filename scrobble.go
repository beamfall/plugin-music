package music

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	scrobble "github.com/beamfall/plugin-sdk/scrobble"
)

const (
	ServiceLastFM              scrobble.Service = "lastfm"
	ServiceListenBrainz        scrobble.Service = "listenbrainz"
	SchemeMusicBrainzRecording                  = "musicbrainz-recording"
	defaultLastFMBaseURL                        = "https://ws.audioscrobbler.com/2.0/"
	defaultListenBrainzBaseURL                  = "https://api.listenbrainz.org"
)

// ErrRateLimited is the sentinel wrapped by a RateLimitedError, distinguishing
// an HTTP 429 response from a generic adapter error so callers can special-case
// backoff timing (e.g. honoring Retry-After) rather than treating it the same
// as a 500. Match it with errors.Is(err, ErrRateLimited).
var ErrRateLimited = errors.New("scrobble: endpoint rate-limited the request")

// RateLimitedError is returned by an adapter's Submit when the endpoint
// responds 429. RetryAfter is the parsed Retry-After duration when the
// endpoint sent one; it is zero when absent or unparseable.
type RateLimitedError struct {
	Service    scrobble.Service
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("scrobble %s: endpoint returned status %d, retry after %s", e.Service, http.StatusTooManyRequests, e.RetryAfter)
	}
	return fmt.Sprintf("scrobble %s: endpoint returned status %d", e.Service, http.StatusTooManyRequests)
}

func (e *RateLimitedError) Unwrap() error { return ErrRateLimited }

// retryAfterDuration parses a Retry-After header, which per RFC 9110 §10.2.3 is
// either a delay in seconds or an HTTP-date. now is injected for testability.
// Returns zero when the header is absent or unparseable.
func retryAfterDuration(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := when.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// scrobbleSubmitError classifies a non-2xx response, returning a
// *RateLimitedError for a 429 (honoring Retry-After when present) and a plain
// error for anything else.
func scrobbleSubmitError(service scrobble.Service, resp *http.Response) error {
	if resp.StatusCode == http.StatusTooManyRequests {
		return &RateLimitedError{Service: service, RetryAfter: retryAfterDuration(resp.Header, time.Now())}
	}
	return fmt.Errorf("scrobble %s: endpoint returned status %d", service, resp.StatusCode)
}

// drainClose reads and closes a response body so the connection can be reused,
// bounding the read so a hostile/huge error body cannot exhaust memory.
func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}

// scrobbleTimestamp returns the Unix-second listen time, falling back to zero
// for a zero WatchedAt (defensive; services treat 0 as missing rather than 1970).
func scrobbleTimestamp(s scrobble.Scrobble) int64 {
	if s.Event.WatchedAt.IsZero() {
		return 0
	}
	return s.Event.WatchedAt.UTC().Unix()
}

func endpointOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	return u.Scheme + "://" + u.Host
}

// --- Last.fm (music) ---------------------------------------------------------

// LastFMAdapter pushes a scrobble to Last.fm's track.scrobble method.
// Last.fm's protocol is a signed form POST: every call carries an api_sig
// = md5(sorted params + shared secret). The api_key and shared secret are the
// operator-registered Last.fm API app credentials; the Profile's session key
// (sk, from the 0600 secure store) authorizes the scrobble as that Profile's
// account.
type LastFMAdapter struct {
	client    scrobble.Doer
	baseURL   string
	apiKey    string
	apiSecret string
}

// NewLastFMAdapter builds the Last.fm adapter. baseURL defaults to the
// production API host; apiKey/apiSecret are the operator's registered Last.fm
// application credentials (NOT per-Profile secrets — the per-Profile session key
// rides Scrobble.Secret).
func NewLastFMAdapter(client scrobble.Doer, baseURL, apiKey, apiSecret string) *LastFMAdapter {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultLastFMBaseURL
	}
	return &LastFMAdapter{client: client, baseURL: baseURL, apiKey: apiKey, apiSecret: apiSecret}
}

func (a *LastFMAdapter) Service() scrobble.Service  { return ServiceLastFM }
func (a *LastFMAdapter) Class() scrobble.MediaClass { return scrobble.ClassAudio }
func (a *LastFMAdapter) IdentitySchemes() []string {
	return []string{SchemeMusicBrainzRecording}
}
func (a *LastFMAdapter) DeclaredEndpoints() []string { return []string{endpointOrigin(a.baseURL)} }

// Submit posts a track.scrobble form. The track title rides the event Title; the
// resolved MusicBrainz recording id rides the mbid param. The api_sig is
// md5 of the params sorted by key, concatenated as name+value, with the shared
// secret appended (Last.fm's documented signing scheme).
func (a *LastFMAdapter) Submit(ctx context.Context, s scrobble.Scrobble) error {
	params := map[string]string{
		"method":    "track.scrobble",
		"api_key":   a.apiKey,
		"sk":        s.Secret,
		"track":     s.Event.Title,
		"mbid":      s.Identity.Value,
		"timestamp": strconv.FormatInt(scrobbleTimestamp(s), 10),
	}
	// Richer outbound metadata: attach artist/album when the
	// Music plugin hydrated them. Empty fields are OMITTED, never sent blank —
	// Last.fm accepts a scrobble carrying a valid recording mbid without an
	// artist, so an un-hydrated track degrades cleanly to the mbid identity.
	if artist := strings.TrimSpace(s.Event.Music.Artist); artist != "" {
		params["artist"] = artist
	}
	if album := strings.TrimSpace(s.Event.Music.Album); album != "" {
		params["album"] = album
	}
	params["api_sig"] = lastfmSignature(params, a.apiSecret)
	params["format"] = "json" // not signed (Last.fm excludes format/callback from the sig)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return scrobbleSubmitError(ServiceLastFM, resp)
	}
	return lastfmEnvelopeError(resp)
}

// lastfmEnvelopeBodyLimit bounds how much of a 2xx response body
// lastfmEnvelopeError reads while checking for Last.fm's documented error
// envelope, so a hostile or huge response cannot exhaust memory.
const lastfmEnvelopeBodyLimit = 1 << 16

// lastfmEnvelopeError reads a bounded slice of a 2xx Last.fm response body and
// returns an adapter error when it carries the documented error envelope, so
// the dispatcher's retry/dead-letter path (scrobble.go:submitWithRetry in
// beamfall-plugin-sdk) treats it as a failed submission rather than delivered.
// The returned error never echoes the response body, tokens, or submitted
// metadata — only Last.fm's numeric error code (or a bounded description of
// why the body couldn't be verified). Anything the adapter cannot positively
// verify as the documented empty/success response — a read error, a body
// truncated at lastfmEnvelopeBodyLimit, JSON that fails to parse, or a body
// that is not a JSON object (e.g. literal `null`) — is treated as a FAILED
// submission, not a silent success: an unreadable, oversized, or non-object
// 2xx body could be hiding the error envelope this function exists to catch.
// Only a genuinely empty body, or a JSON object with no "error" key or
// error==0, is success.
func lastfmEnvelopeError(resp *http.Response) error {
	if resp.Body == nil {
		return nil
	}
	// Read one byte past the cap so truncation is detectable: if we get back
	// more than lastfmEnvelopeBodyLimit bytes, the real body was longer than
	// what we read and buffer, and we can't rule out a truncated envelope.
	body, err := io.ReadAll(io.LimitReader(resp.Body, lastfmEnvelopeBodyLimit+1))
	if err != nil {
		// Never wrap the raw reader error: a hostile or injected body reader can
		// return unbounded or sensitive text in its error value. A static,
		// bounded message keeps the failure observable without leaking that.
		return fmt.Errorf("scrobble %s: could not read response body, cannot verify delivery", ServiceLastFM)
	}
	if len(body) > lastfmEnvelopeBodyLimit {
		return fmt.Errorf("scrobble %s: response body exceeded %d bytes, cannot verify delivery", ServiceLastFM, lastfmEnvelopeBodyLimit)
	}
	if len(body) == 0 {
		return nil // documented empty success body
	}
	// Decode into a map so we positively verify the body is a JSON *object*:
	// map decoding accepts only `{...}` or `null`, and a bare `null` decodes to
	// a nil map without error — which would otherwise read as error==0 and
	// masquerade as the documented success body. Last.fm's failure shape is
	// {"error":N,...} (track.scrobble error codes 1-29); a success body carries
	// no "error" key.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		// JSON decoder errors can include fragments derived from the response.
		// Keep every unverifiable-body error static and bounded.
		return fmt.Errorf("scrobble %s: malformed response body, cannot verify delivery", ServiceLastFM)
	}
	if fields == nil {
		return fmt.Errorf("scrobble %s: response body was JSON null, cannot verify delivery", ServiceLastFM)
	}
	rawError, ok := fields["error"]
	if !ok {
		return nil // documented success body carries no "error" key
	}
	if bytes.Equal(bytes.TrimSpace(rawError), []byte("null")) {
		return fmt.Errorf("scrobble %s: null error code, cannot verify delivery", ServiceLastFM)
	}
	var code int
	if err := json.Unmarshal(rawError, &code); err != nil {
		// Do not wrap the decoder error: numeric overflow and type errors may echo
		// attacker-controlled literals from the response body.
		return fmt.Errorf("scrobble %s: malformed error code, cannot verify delivery", ServiceLastFM)
	}
	if code == 0 {
		return nil
	}
	return fmt.Errorf("scrobble %s: endpoint returned success status with error code %d", ServiceLastFM, code)
}

// lastfmSignature computes Last.fm's api_sig: md5 over the params sorted by key,
// concatenated as key+value with no separator, with the shared secret appended.
// format and callback are excluded (Last.fm's documented rule). The secret is
// used transiently here and never logged.
func lastfmSignature(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "format" || k == "callback" || k == "api_sig" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(params[k])
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// --- ListenBrainz (music) ----------------------------------------------------

// ListenBrainzAdapter pushes a single "listen" to ListenBrainz's submit-listens
// endpoint. Authorization is the Profile's user token (from the
// 0600 secure store) as a "Token <token>" header.
type ListenBrainzAdapter struct {
	client  scrobble.Doer
	baseURL string
}

// NewListenBrainzAdapter builds the ListenBrainz adapter. baseURL defaults to the
// production API host; tests point it at an httptest server.
func NewListenBrainzAdapter(client scrobble.Doer, baseURL string) *ListenBrainzAdapter {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultListenBrainzBaseURL
	}
	return &ListenBrainzAdapter{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func (a *ListenBrainzAdapter) Service() scrobble.Service  { return ServiceListenBrainz }
func (a *ListenBrainzAdapter) Class() scrobble.MediaClass { return scrobble.ClassAudio }
func (a *ListenBrainzAdapter) IdentitySchemes() []string {
	return []string{SchemeMusicBrainzRecording}
}
func (a *ListenBrainzAdapter) DeclaredEndpoints() []string {
	return []string{endpointOrigin(a.baseURL)}
}

// Submit posts a single listen. The track name rides the event Title; the
// resolved MusicBrainz recording id rides additional_info.recording_mbid.
func (a *ListenBrainzAdapter) Submit(ctx context.Context, s scrobble.Scrobble) error {
	// Richer outbound metadata: the recording mbid is the
	// identity gate (always present); artist/release names + their mbids ride
	// the submission when the Music plugin hydrated them, and are OMITTED when
	// absent so an un-hydrated track degrades to the recording-mbid-only listen.
	additionalInfo := map[string]any{
		"recording_mbid": s.Identity.Value,
	}
	if artistMBID := strings.TrimSpace(s.Event.Music.ArtistMBID); artistMBID != "" {
		additionalInfo["artist_mbids"] = []string{artistMBID}
	}
	if releaseMBID := strings.TrimSpace(s.Event.Music.ReleaseMBID); releaseMBID != "" {
		additionalInfo["release_mbid"] = releaseMBID
	}
	trackMetadata := map[string]any{
		"track_name":      s.Event.Title,
		"additional_info": additionalInfo,
	}
	if artist := strings.TrimSpace(s.Event.Music.Artist); artist != "" {
		trackMetadata["artist_name"] = artist
	}
	if album := strings.TrimSpace(s.Event.Music.Album); album != "" {
		trackMetadata["release_name"] = album
	}
	payload := map[string]any{
		"listen_type": "single",
		"payload": []any{
			map[string]any{
				"listened_at":    scrobbleTimestamp(s),
				"track_metadata": trackMetadata,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/1/submit-listens", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token "+s.Secret)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return scrobbleSubmitError(ServiceListenBrainz, resp)
	}
	return nil
}

// --- Factory functions -------------------------------------------------------

// LastFMScrobbleFactory builds a LastFMAdapter from operator config. A
// SecretSource that resolves a ref to an empty string without an error (e.g. a
// stub or a misconfigured store) must not silently produce an adapter that
// signs every request with a blank api_key/api_secret, so both are checked for
// non-empty content after resolution.
func LastFMScrobbleFactory(client scrobble.Doer, cfg scrobble.AdapterConfig, secrets scrobble.SecretSource) (scrobble.Adapter, error) {
	apiKey, err := secrets.Secret(cfg.APIKeyRef)
	if err != nil {
		return nil, fmt.Errorf("scrobble lastfm adapter: api key: %w", err)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("scrobble lastfm adapter: api key: resolved empty for ref %q", cfg.APIKeyRef)
	}
	apiSecret, err := secrets.Secret(cfg.APISecretRef)
	if err != nil {
		return nil, fmt.Errorf("scrobble lastfm adapter: api secret: %w", err)
	}
	if strings.TrimSpace(apiSecret) == "" {
		return nil, fmt.Errorf("scrobble lastfm adapter: api secret: resolved empty for ref %q", cfg.APISecretRef)
	}
	return NewLastFMAdapter(client, cfg.BaseURL, apiKey, apiSecret), nil
}

// ListenBrainzScrobbleFactory builds a ListenBrainzAdapter from operator config.
// secrets is unused: ListenBrainz auth is a per-profile bearer token supplied at
// submit time (see ListenBrainzAdapter.Submit), not at factory construction; the
// parameter is required by the scrobble.AdapterFactory contract (scrobble.go:700
// in beamfall-plugin-sdk).
func ListenBrainzScrobbleFactory(client scrobble.Doer, cfg scrobble.AdapterConfig, _ scrobble.SecretSource) (scrobble.Adapter, error) {
	return NewListenBrainzAdapter(client, cfg.BaseURL), nil
}
