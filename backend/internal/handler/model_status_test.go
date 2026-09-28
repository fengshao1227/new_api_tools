package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
