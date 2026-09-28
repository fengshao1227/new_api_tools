package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func serveWithCORS(t *testing.T, method, host, origin string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORSMiddleware())
	r.Any("/api/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	req := httptest.NewRequest(method, "/api/ping", nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCORSAllowsSameOriginRequests(t *testing.T) {
	cases := []struct{ name, host, origin string }{
		{"no origin header", "tool.example.com", ""},
		{"exact origin", "tool.example.com", "https://tool.example.com"},
		{"proxy dropped the port from Host", "203.0.113.7", "http://203.0.113.7:1145"},
		{"host carries the port", "tool.example.com:8443", "https://tool.example.com:8443"},
	}
	for _, tc := range cases {
		rec := serveWithCORS(t, http.MethodPost, tc.host, tc.origin)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", tc.name, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("%s: credentials must never be allowed, got %q", tc.name, got)
		}
	}
}

func TestCORSRejectsCrossOriginRequests(t *testing.T) {
	for _, origin := range []string{"https://evil.example", "null", "https://tool.example.com.evil.example"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
			rec := serveWithCORS(t, method, "tool.example.com", origin)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s: status %d, want 403", method, origin, rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("%s %s: must not echo an allowed origin, got %q", method, origin, got)
			}
		}
	}
}
