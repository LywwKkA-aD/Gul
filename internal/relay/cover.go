package relay

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// A relay that answers differently for every kind of wrong request describes
// its own decision tree to whoever asks.
//
// Measured against the production host on 2026-08-26: ten distinguishable
// answers, several with wording of our own, and one that named the software
// outright - `GET /mumble` came back with
// `WWW-Authenticate: Bearer realm="gul-relay"`. The front page returned Go's
// bare `404 page not found` with no Server header at all, which no ordinary
// site does. Identifying the host cost one request.
//
// So the relay stops answering as a relay. Everything that is not a valid
// tunnel request gets the same page an ordinary site gives for an address that
// does not exist, byte for byte, and the front page is a real page. The
// default pair is a stock nginx install, which is the most numerous thing on
// the public internet and therefore the least interesting.
const (
	defaultServerHeader = "nginx"
	// The stock nginx pages, reproduced exactly. Their value is that they are
	// unremarkable: a host that serves them looks like one of the millions
	// that were installed and never configured.
	defaultIndexPage = `<html>
<head><title>Welcome to nginx!</title></head>
<body>
<h1>Welcome to nginx!</h1>
<p>If you see this page, the nginx web server is successfully installed and
working. Further configuration is required.</p>

<p>For online documentation and support please refer to
<a href="http://nginx.org/">nginx.org</a>.<br/>
Commercial support is available at
<a href="http://nginx.com/">nginx.com</a>.</p>

<p><em>Thank you for using nginx.</em></p>
</body>
</html>
`
	defaultNotFoundPage = `<html>
<head><title>404 Not Found</title></head>
<body>
<center><h1>404 Not Found</h1></center>
<hr><center>nginx</center>
</body>
</html>
`
)

// NewCover builds the ordinary website on its own, for a caller that mounts it
// beside the tunnel. Empty fields take the stock pages.
func NewCover(server, index, notFound string) http.Handler {
	return newCoverSite(server, index, notFound, time.Time{})
}

// coverSite is the ordinary website the relay presents to everything that is
// not a tunnel request.
type coverSite struct {
	server   string
	index    []byte
	notFound []byte
	// indexETag and modified make the front page cacheable the way a static
	// file served by a web server is. A page with no validators at all is its
	// own small anomaly.
	indexETag string
	modified  time.Time
}

// coverModified is the front page's modification time when the caller supplies
// none. It is fixed, not the process start time: a Last-Modified - and the
// ETag derived from it - that changed on every restart is a value no static
// file produces, and a censor comparing two probes an hour apart would see it
// move. An ordinary past date.
var coverModified = time.Date(2024, time.March, 4, 9, 21, 14, 0, time.UTC)

// newCoverSite builds the site. Empty fields take the stock pages, and a
// caller that wants a real site of its own passes its own bytes.
func newCoverSite(server, index, notFound string, modified time.Time) *coverSite {
	if server == "" {
		server = defaultServerHeader
	}
	if index == "" {
		index = defaultIndexPage
	}
	if notFound == "" {
		notFound = defaultNotFoundPage
	}
	if modified.IsZero() {
		modified = coverModified
	}
	modified = modified.UTC().Truncate(time.Second)
	return &coverSite{
		server:   server,
		index:    []byte(index),
		notFound: []byte(notFound),
		// nginx's ETag is hex(mtime)-hex(length), not a hash of the body
		// (ngx_http_set_etag). Matching the shape is the point: an ETag of
		// sixteen bytes with no dash is a value no nginx emits.
		indexETag: fmt.Sprintf(`"%x-%x"`, modified.Unix(), len(index)),
		modified:  modified,
	}
}

// decorate puts the server identity on a response. Every answer the relay
// gives goes through here, including the ones it gives to its own clients:
// one host must not have two personalities.
func (c *coverSite) decorate(w http.ResponseWriter) {
	w.Header().Set("Server", c.server)
}

// setETag writes the header under the spelling every other server on the web
// uses.
//
// Header.Set would canonicalise it to "Etag", because Go capitalises after
// hyphens and "ETag" has none. Nothing else emits that spelling: RFC 9110 names
// the field ETag, nginx and Apache send ETag, and a lone "Etag" on the wire
// says "this is a Go program" in four characters - on a host whose whole
// purpose is to look like a stock nginx install.
//
// Measured against production on 2026-08-27, before this: our front page came
// back with "Etag:" while nginx.org came back with "ETag:". The value beside it
// was already shaped like nginx's hex(mtime)-hex(length) on purpose. The shape
// was right and the name was wrong, and no test could see it, because every
// test read the header map - where the key is whatever Set canonicalised it to
// - instead of the bytes.
//
// Assigning the map entry directly is the documented way past the
// canonicalisation: net/http writes back exactly the key it was given.
func setETag(w http.ResponseWriter, value string) {
	w.Header()["ETag"] = []string{value}
}

// Error writes the page nginx gives for a status, in the same shape as the 404
// above.
//
// It exists because two of the relay's answers were leaving as Go's own:
// http.Error writes "text/plain; charset=utf-8" and adds
// X-Content-Type-Options: nosniff, so a host claiming to be a stock nginx
// install answered its 429 and its 503 in a voice nothing on that host uses
// anywhere else. Only somebody who already knows the derived path reaches
// them - the ban counter sits behind that check - so this was never the first
// thing an outsider saw. It was still one host with two personalities, which
// is the thing the cover site exists to prevent.
func (c *coverSite) Error(w http.ResponseWriter, r *http.Request, status int) {
	c.decorate(w)
	page := c.errorPage(status)
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Content-Length", strconv.Itoa(len(page)))
	w.WriteHeader(status)
	if r != nil && r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(page)
}

// errorPage renders nginx's error body for a status. nginx builds every one of
// them from the same template, so one renderer covers them all.
func (c *coverSite) errorPage(status int) []byte {
	if status == http.StatusNotFound {
		return c.notFound
	}
	line := strconv.Itoa(status) + " " + http.StatusText(status)
	return fmt.Appendf(nil, `<html>
<head><title>%s</title></head>
<body>
<center><h1>%s</h1></center>
<hr><center>%s</center>
</body>
</html>
`, line, line, c.server)
}

// ServeHTTP is the site itself: a front page, and the same "not found" for
// everything else.
func (c *coverSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		c.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		c.NotFound(w, r)
		return
	}
	c.decorate(w)
	// On a 304 nginx returns only the validators - Server, Date, Last-Modified,
	// ETag - and no Content-Type, so those two are set here, before the
	// conditional check, and Content-Type is set only on the body path below.
	w.Header().Set("Last-Modified", c.modified.Format(http.TimeFormat))
	setETag(w, c.indexETag)
	// No Accept-Ranges, and ranges are not honoured: this is the `max_ranges 0`
	// nginx personality, a real and consistent one. Advertising byte ranges
	// while answering a Range request with the whole body - which is what
	// setting the header without handling it would do - is something no nginx
	// config produces, and that inconsistency is a sharper tell than a missing
	// header.
	if match := r.Header.Get("If-None-Match"); match == c.indexETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Content-Length", strconv.Itoa(len(c.index)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(c.index)
}

// NotFound is the single answer to every request the relay refuses, whatever
// the reason: a path that is not the tunnel, a method it does not take, a
// missing credential, a wrong one. The reason is in the log, not on the wire.
//
// A body is written for HEAD too - net/http drops it - so the Content-Length
// matches what a real server would report.
func (c *coverSite) NotFound(w http.ResponseWriter, r *http.Request) {
	c.decorate(w)
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Content-Length", strconv.Itoa(len(c.notFound)))
	w.WriteHeader(http.StatusNotFound)
	if r != nil && r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(c.notFound)
}
