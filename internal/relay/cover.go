package relay

import (
	"crypto/sha256"
	"encoding/hex"
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
		modified = time.Now().UTC().Truncate(time.Second)
	}
	sum := sha256.Sum256([]byte(index))
	return &coverSite{
		server:    server,
		index:     []byte(index),
		notFound:  []byte(notFound),
		indexETag: `"` + hex.EncodeToString(sum[:8]) + `"`,
		modified:  modified.UTC().Truncate(time.Second),
	}
}

// decorate puts the server identity on a response. Every answer the relay
// gives goes through here, including the ones it gives to its own clients:
// one host must not have two personalities.
func (c *coverSite) decorate(w http.ResponseWriter) {
	w.Header().Set("Server", c.server)
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
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Last-Modified", c.modified.Format(http.TimeFormat))
	w.Header().Set("ETag", c.indexETag)
	w.Header().Set("Accept-Ranges", "bytes")
	if match := r.Header.Get("If-None-Match"); match == c.indexETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
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
