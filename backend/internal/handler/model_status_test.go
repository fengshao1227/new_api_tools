package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/database"
)

func TestModelStatusBatchRejectsTooManyModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterModelStatusRoutes(r.Group("/api"))

	names := make([]string, maxModelStatusBatch+1)
	for i := range names {
		names[i] = fmt.Sprintf("model-%d", i)
	}
	payload, _ := json.Marshal(names)

	for _, path := range []string{"/api/model-status/status/batch", "/api/model-status/status/multiple"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "TOO_MANY_MODELS") {
			t.Fatalf("%s: status %d body %s", path, rec.Code, rec.Body.String())
		}
	}
}

// The public embed page is gone, and with it its theme and site title.
func TestModelStatusEmbedSettingsAreGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database.SetForTesting(&database.Manager{})
	r := gin.New()
	RegisterModelStatusRoutes(r.Group("/api"))

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/model-status/config/theme"},
		{http.MethodPut, "/api/model-status/config/theme"},
		{http.MethodPost, "/api/model-status/config/theme"},
		{http.MethodGet, "/api/model-status/config/site-title"},
		{http.MethodPut, "/api/model-status/config/site-title"},
		{http.MethodPost, "/api/model-status/config/site-title"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s should no longer be routed, got %d", route.method, route.path, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/model-status/selected", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("selected: %d %s", rec.Code, rec.Body.String())
	}
	for _, key := range []string{"theme", "site_title"} {
		if _, ok := body[key]; ok {
			t.Errorf("selected still returns %q", key)
		}
	}
	if _, ok := body["time_window"]; !ok {
		t.Errorf("selected lost its own settings: %v", body)
	}
}
