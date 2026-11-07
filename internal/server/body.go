package server

import (
	"net/http"
	"strings"
)

// Larger bodies are reserved for the three bounded, authenticated site-write
// endpoints. Account requests and unknown routes keep the original 64 KiB cap.
func requestBodyLimit(r *http.Request) int64 {
	const ordinary = 64 << 10
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "api" || parts[2] != "sites" || r.URL.RawPath != "" {
		return ordinary
	}
	id := parts[3]
	if len(id) != 16 && len(id) != 32 {
		return ordinary
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ordinary
		}
	}
	switch {
	case r.Method == http.MethodPost && parts[4] == "upload":
		return 2 << 20
	case r.Method == http.MethodPut && parts[4] == "content":
		return 512 << 10
	case r.Method == http.MethodPost && parts[4] == "tls":
		return 128 << 10
	default:
		return ordinary
	}
}
