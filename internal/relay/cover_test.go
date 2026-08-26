package relay

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
		request := httptest.NewRequest(http.MethodGet, "https://"+testHost+Path, nil)
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
	request := httptest.NewRequest(http.MethodGet, "https://"+testHost+Path, nil)
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
	for _, header := range []string{"ETag", "Last-Modified", "Content-Length"} {
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
