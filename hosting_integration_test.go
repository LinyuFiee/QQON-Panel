package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHostingEndpointsRequireLoginAndCSRF(t *testing.T) {
	app := newIntegrationApp(t)
	for _, path := range []string{"/api/management", "/api/apps", "/api/sites", "/api/jobs"} {
		if r := integrationRequest(app.handler, "GET", path, nil, nil, ""); r.Code != 401 {
			t.Fatalf("unprotected %s: %d", path, r.Code)
		}
	}
	cookies, csrf := initializeIntegration(t, app)
	for _, path := range []string{"/api/management", "/api/apps", "/api/sites", "/api/jobs"} {
		response := integrationRequest(app.handler, "GET", path, nil, cookies, "")
		if response.Code != 200 || !json.Valid(response.Body.Bytes()) {
			t.Fatalf("capability %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	operations := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/apps/nginx/install", map[string]any{"confirmed": true}},
		{"POST", "/api/apps/redis/start", map[string]any{"confirmed": true}},
		{"POST", "/api/sites", map[string]any{"confirmed": true, "site": map[string]any{"name": "isolated", "domains": []string{"web.example.test"}, "type": "static", "port": 8081, "enabled": true}}},
		{"PUT", "/api/sites/0123456789abcdef0123456789abcdef/content", map[string]any{"confirmed": true, "path": "index.html", "content": "test"}},
	}
	for _, op := range operations {
		t.Run(op.method+op.path, func(t *testing.T) {
			if response := integrationRequest(app.handler, op.method, op.path, op.body, cookies, ""); response.Code != http.StatusForbidden {
				t.Fatalf("missing CSRF accepted: %d", response.Code)
			}
			response := integrationRequest(app.handler, op.method, op.path, op.body, cookies, csrf)
			if response.Code != http.StatusForbidden && response.Code != http.StatusServiceUnavailable && response.Code != http.StatusNotImplemented {
				t.Fatalf("operation without system consent: %d %s", response.Code, response.Body.String())
			}
		})
	}
	jobs := integrationRequest(app.handler, "GET", "/api/jobs", nil, cookies, "")
	var view struct {
		Jobs []json.RawMessage `json:"jobs"`
	}
	if err := json.Unmarshal(jobs.Body.Bytes(), &view); err != nil || len(view.Jobs) != 0 {
		t.Fatalf("denied operations queued work: %s", jobs.Body.String())
	}
}
