package jobrunner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListOmitsLogPayloadButDetailRetainsIt(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	task, err := r.Submit("test.log", "fixture", time.Second, func(_ context.Context, progress *Progress) error {
		progress.Log("bounded-log-detail-marker")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitTask(t, r, task.ID, "succeeded")
	mux := http.NewServeMux()
	r.Register(mux)
	list := httptest.NewRecorder()
	mux.ServeHTTP(list, httptest.NewRequest("GET", "/api/jobs", nil))
	if list.Code != 200 || strings.Contains(list.Body.String(), "bounded-log-detail-marker") || !strings.Contains(list.Body.String(), `"logs":[]`) {
		t.Fatalf("polling response includes logs or loses array contract: %s", list.Body.String())
	}
	detail := httptest.NewRecorder()
	mux.ServeHTTP(detail, httptest.NewRequest("GET", "/api/jobs/"+task.ID, nil))
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "bounded-log-detail-marker") {
		t.Fatal("on-demand log detail missing")
	}
}
