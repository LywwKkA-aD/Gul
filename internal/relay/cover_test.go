package relay

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// coverWire fetches a path over a real connection and returns the response head
// exactly as it left the socket, plus the body.
//
// The header map is not the wire. Header.Set canonicalises the key it files a
// value under and Header.Get canonicalises the key it looks up, so a test that
// asks the map for "ETag" is answered about whatever Set stored - which is how
// production served "Etag:" while every test here agreed it served "ETag".
// Measured against nginx.org on 2026-08-27: they send "ETag", we sent "Etag",
// and four characters said "this is a Go program" on a host whose whole job is
// to look like a stock nginx install.
func coverWire(t *testing.T, path string, request http.Header) (head, body string) {
	t.Helper()
	server := httptest.NewServer(NewCover("", "", ""))
	t.Cleanup(server.Close)

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial cover: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	var out strings.Builder
	fmt.Fprintf(&out, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", path, testHost)
	for name, values := range request {
		for _, value := range values {
			fmt.Fprintf(&out, "%s: %s\r\n", name, value)
		}
	}
	out.WriteString("\r\n")
	if _, err := io.WriteString(conn, out.String()); err != nil {
		t.Fatalf("write request: %v", err)
	}

	raw, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	whole := string(raw)
	if split := strings.Index(whole, "\r\n\r\n"); split >= 0 {
		return whole[:split], whole[split+4:]
	}
	return whole, ""
}

// wireHeader reads one header off the wire by its exact spelling.
func wireHeader(head, name string) string {
	for line := range strings.SplitSeq(head, "\r\n") {
		prefix := name + ": "
		if after, ok := strings.CutPrefix(line, prefix); ok {
			return after
		}
	}
	return ""
}

// coverGet fetches a path from a fresh cover site and returns the recorder.
func coverGet(path string, header http.Header) *httptest.ResponseRecorder {
	cover := NewCover("", "", "")
	r := httptest.NewRequest(http.MethodGet, "https://"+testHost+path, nil)
	maps.Copy(r.Header, header)
	w := httptest.NewRecorder()
	cover.ServeHTTP(w, r)
	return w
}

// The front page's validators have to look like a static file served by
// nginx, or an active prober that knows nginx's formats sees straight through
// the disguise. These were the tells found in review.
func TestCoverFrontPageMatchesNginxValidators(t *testing.T) {
	t.Parallel()
	w := coverGet("/", nil)

	// nginx's ETag is hex(mtime)-hex(length): two hex runs joined by a dash.
	// Sixteen hex characters with no dash is a value no nginx produces.
	head, _ := coverWire(t, "/", nil)
	etag := wireHeader(head, "ETag")
	if !regexp.MustCompile(`^"[0-9a-f]+-[0-9a-f]+"$`).MatchString(etag) {
		t.Errorf("ETag = %q, want the nginx hex(mtime)-hex(len) shape", etag)
	}

	// Last-Modified must be the fixed cover date, not the process start time -
	// a value derived from time.Now would move between two probes an hour
	// apart, which no static file does. Pinning it to the constant catches any
	// clock-derived value, which a same-second stability check would not.
	want := coverModified.UTC().Format(http.TimeFormat)
	if got := w.Header().Get("Last-Modified"); got != want {
		t.Errorf("Last-Modified = %q, want the fixed %q", got, want)
	}
}

// nginx answers a matching conditional request with only the validators, no
// Content-Type. Sending Content-Type on a 304 is a tell.
func TestCoverConditionalRequestOmitsContentType(t *testing.T) {
	t.Parallel()
	head, _ := coverWire(t, "/", nil)
	head304, _ := coverWire(t, "/", http.Header{"If-None-Match": {wireHeader(head, "ETag")}})

	if !strings.HasPrefix(head304, "HTTP/1.1 304 ") {
		t.Fatalf("status line = %q, want 304", strings.SplitN(head304, "\r\n", 2)[0])
	}
	if got := wireHeader(head304, "Content-Type"); got != "" {
		t.Errorf("304 carries Content-Type %q; nginx sends none", got)
	}
	if wireHeader(head304, "ETag") == "" || wireHeader(head304, "Last-Modified") == "" {
		t.Error("304 dropped the validators nginx keeps")
	}
}

// One host, one voice — checked on the bytes, because the bytes are the only
// place the difference exists.
//
// Go canonicalises a header key by capitalising after hyphens, and "ETag" has
// none, so Header.Set files it as "Etag" and Header.Get is asked about "Etag"
// too. Both halves of every earlier test agreed with each other and neither
// agreed with the socket. Production served "Etag:" for as long as the cover
// site has existed; nginx, Apache and RFC 9110 all say "ETag".
func TestTheWireSpellsHeadersTheWayServersDo(t *testing.T) {
	t.Parallel()
	head, _ := coverWire(t, "/", nil)

	if !strings.Contains(head, "\r\nETag: ") {
		t.Errorf("no ETag on the wire:\n%s", head)
	}
	// The tell itself. Nothing but a Go program emits this spelling.
	if strings.Contains(head, "\r\nEtag: ") {
		t.Errorf("the wire carried Go's \"Etag\":\n%s", head)
	}
}

// The refusals a censor can actually reach have to be in the same voice as
// everything else. http.Error answers in Go's: "text/plain; charset=utf-8"
// plus X-Content-Type-Options: nosniff, neither of which this host uses
// anywhere. Reaching them needs the derived path, so this was never the first
// thing an outsider saw - it was still one host with two personalities.
func TestRejectionsWearTheSameFaceAsTheCover(t *testing.T) {
	t.Parallel()
	for name, status := range map[string]int{
		"too many requests": http.StatusTooManyRequests,
		"no capacity":       http.StatusServiceUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cover := NewCover("", "", "").(*coverSite)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			// Through the functions the refusals actually go through, not
			// through the renderer underneath them: the bug was in the choice
			// of writer, and a test that calls the right writer directly
			// cannot see a caller still using the wrong one.
			switch status {
			case http.StatusTooManyRequests:
				writeRateLimited(cover, w, r, time.Minute)
			case http.StatusServiceUnavailable:
				writeCapacityRejected(cover, w, r, time.Minute)
			}

			if got := w.Header().Get("Retry-After"); got == "" {
				t.Error("no Retry-After; a client that honours it waits instead of hammering")
			}

			if w.Code != status {
				t.Fatalf("status = %d, want %d", w.Code, status)
			}
			if got := w.Header().Get("Content-Type"); got != "text/html" {
				t.Errorf("Content-Type = %q, want the text/html this host serves everywhere", got)
			}
			if got := w.Header().Get("X-Content-Type-Options"); got != "" {
				t.Errorf("X-Content-Type-Options = %q; nothing else on this host sends it", got)
			}
			// nginx builds every error page from one template, footer and all.
			body := w.Body.String()
			if !strings.Contains(body, "<hr><center>"+defaultServerHeader+"</center>") {
				t.Errorf("body is not the page nginx would give:\n%s", body)
			}
			if !strings.Contains(body, http.StatusText(status)) {
				t.Errorf("body does not name the status:\n%s", body)
			}
		})
	}
}

// The cover is the max_ranges-0 nginx personality: it does not advertise byte
// ranges and does not honour them. Advertising them while returning the whole
// body - the previous behaviour - is something no nginx config produces.
func TestCoverDoesNotAdvertiseOrHonourRanges(t *testing.T) {
	t.Parallel()
	if got := coverGet("/", nil).Header().Get("Accept-Ranges"); got != "" {
		t.Errorf("Accept-Ranges = %q, want it absent", got)
	}
	w := coverGet("/", http.Header{"Range": {"bytes=0-0"}})
	if w.Code != http.StatusOK {
		t.Fatalf("range request status = %d, want a plain 200", w.Code)
	}
	if got := w.Header().Get("Content-Range"); got != "" {
		t.Errorf("Content-Range = %q, want none when ranges are not offered", got)
	}
	if w.Body.Len() <= 1 {
		t.Errorf("range request got a %d-byte body; the whole page was expected", w.Body.Len())
	}
}

// A prober must not be able to tell one refusal from another.
//
// Measured against the production host on 2026-08-26, before this existed: ten
// distinguishable answers, and `GET /mumble` came back with
// `WWW-Authenticate: Bearer realm="gul-relay"`, naming the software to whoever
// asked. Every refusal now has to be the same bytes as an address that simply
// does not exist.
func TestEveryRefusalIsIndistinguishable(t *testing.T) {
	t.Parallel()
	h := mustHandler(t, baseConfig("server secret"))

	refusal := func(build func(*http.Request)) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "https://"+testHost+testPath(), nil)
		request.Host = testHost
		request.RemoteAddr = "192.0.2.10:12345"
		build(request)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		return response
	}

	baseline := refusal(func(r *http.Request) { r.URL.Path = "/does-not-exist" })
	cases := map[string]func(*http.Request){
		"the tunnel path with no credential": func(*http.Request) {},
		"a wrong credential": func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer definitely-wrong")
		},
		"a credential of the wrong shape": func(r *http.Request) {
			r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		},
		"a method the tunnel does not take": func(r *http.Request) { r.Method = http.MethodPost },
		"a query string":                    func(r *http.Request) { r.URL.RawQuery = "target=elsewhere" },
		"another host":                      func(r *http.Request) { r.Host = "other.example.test" },
		"a browser origin": func(r *http.Request) {
			r.Header.Set("Origin", "https://evil.example.test")
		},
		"a valid credential without the subprotocol": func(r *http.Request) {
			r.Header.Set("Authorization", testCredential("server secret").Header())
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			got := refusal(build)
			if got.Code != baseline.Code {
				t.Errorf("status = %d, want %d like any unknown address", got.Code, baseline.Code)
			}
			if !bytes.Equal(got.Body.Bytes(), baseline.Body.Bytes()) {
				t.Errorf("body = %q, want %q", got.Body.String(), baseline.Body.String())
			}
			for key, want := range baseline.Header() {
				if strings.EqualFold(key, "Date") {
					continue
				}
				if have := got.Header().Values(key); !equalStrings(have, want) {
					t.Errorf("header %s = %v, want %v", key, have, want)
				}
			}
			for key := range got.Header() {
				if strings.EqualFold(key, "Date") || strings.EqualFold(key, "Connection") {
					continue
				}
				if _, ok := baseline.Header()[key]; !ok {
					t.Errorf("header %s is only present on this refusal", key)
				}
			}
		})
	}
}

// The refusal must not carry anything that names the software, whichever
// request produced it.
func TestRefusalNamesNothing(t *testing.T) {
	t.Parallel()
	h := mustHandler(t, baseConfig("server secret"))
	request := httptest.NewRequest(http.MethodGet, "https://"+testHost+testPath(), nil)
	request.Host = testHost
	request.RemoteAddr = "192.0.2.10:12345"
	response := httptest.NewRecorder()

	h.ServeHTTP(response, request)

	if got := response.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q; it announced the service by name", got)
	}
	rendered := strings.ToLower(response.Header().Get("Server") + response.Body.String())
	for _, word := range []string{"gul", "mumble", "relay", "bearer", "go-http"} {
		if strings.Contains(rendered, word) {
			t.Errorf("the refusal contains %q", word)
		}
	}
}

// The front page has to be a page: a host that answers 404 to everything,
// including its own root, is its own kind of odd.
func TestCoverSiteServesAFrontPage(t *testing.T) {
	t.Parallel()
	cover := NewCover("", "", "")
	request := httptest.NewRequest(http.MethodGet, "https://"+testHost+"/", nil)
	response := httptest.NewRecorder()

	cover.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("front page status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Server"); got == "" {
		t.Error("no Server header; a response without one is unusual on its own")
	}
	head, _ := coverWire(t, "/", nil)
	if wireHeader(head, "ETag") == "" {
		t.Error("no ETag; a served file carries one")
	}
	for _, header := range []string{"Last-Modified", "Content-Length"} {
		if response.Header().Get(header) == "" {
			t.Errorf("no %s; a served file carries one", header)
		}
	}
	if !strings.Contains(response.Body.String(), "<html>") {
		t.Errorf("front page body = %q, want a page", response.Body.String())
	}
}

// A caller that has a real site of its own must be able to use it.
func TestCoverSiteAcceptsItsOwnPages(t *testing.T) {
	t.Parallel()
	cover := NewCover("Apache", "<html>mine</html>", "<html>gone</html>")

	index := httptest.NewRecorder()
	cover.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "https://"+testHost+"/", nil))
	if got := index.Body.String(); got != "<html>mine</html>" {
		t.Errorf("front page = %q", got)
	}
	if got := index.Header().Get("Server"); got != "Apache" {
		t.Errorf("Server = %q, want Apache", got)
	}

	missing := httptest.NewRecorder()
	cover.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "https://"+testHost+"/x", nil))
	if got := missing.Body.String(); got != "<html>gone</html>" {
		t.Errorf("not found page = %q", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
