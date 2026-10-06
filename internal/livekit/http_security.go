package livekit

import (
	"errors"
	"net/http"
)

// SDK 2.18.1 uses http.DefaultClient for /rtc/validate after a WebSocket
// failure and provides no client injection. Preserve that client's settings,
// restricting only redirects of the SDK's authenticated validation request.
// Installed once at package initialization, before SDK transport goroutines.
func guardSDKHTTPClient(original *http.Client) *http.Client {
	guarded := *original
	guarded.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > 0 && via[0].URL.Path == "/rtc/validate" && via[0].Header.Get("Authorization") != "" {
			return http.ErrUseLastResponse
		}
		if original.CheckRedirect != nil {
			return original.CheckRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &guarded
}
