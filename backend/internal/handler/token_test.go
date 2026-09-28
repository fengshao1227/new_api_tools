package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/logger"
	"github.com/new-api-tools/backend/internal/service"
	_ "modernc.org/sqlite"
)

// installTokenRouter serves the token routes over an in-memory tokens table,
// as the password login ("admin") would call them.
func installTokenRouter(t *testing.T) *gin.Engine {
	t.Helper()
	if logger.L == nil {
		logger.Init("error", "")
	}
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE tokens (id INTEGER PRIMARY KEY, status INTEGER, deleted_at INTEGER)`,
		`INSERT INTO tokens VALUES (1, 1, NULL)`,
		`INSERT INTO tokens VALUES (2, 2, NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	database.SetForTesting(&database.Manager{DB: db})
	t.Cleanup(service.SetTokenAuditPathForTest(filepath.Join(t.TempDir(), "token_audit.jsonl")))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("auth_method", "jwt")
		c.Set("user_sub", "admin")
		c.Next()
	})
	RegisterTokenRoutes(r.Group("/api"))
	return r
}

func serveJSON(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func readTokenAudit(t *testing.T, r *gin.Engine) []service.TokenAuditEntry {
	t.Helper()
	rec := serveJSON(t, r, http.MethodGet, "/api/tokens/audit?limit=5", "")
	var body struct {
		Success bool                      `json:"success"`
		Data    []service.TokenAuditEntry `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Success {
		t.Fatalf("audit response %d: %s", rec.Code, rec.Body.String())
	}
	return body.Data
}

func TestBatchTokenOperationsAreAudited(t *testing.T) {
	r := installTokenRouter(t)

	rec := serveJSON(t, r, http.MethodPost, "/api/tokens/batch-disable", `{"ids":[1,2]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"audit_recorded":true`) {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	rec = serveJSON(t, r, http.MethodPost, "/api/tokens/batch-enable", `{"ids":[2]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body.String())
	}

	entries := readTokenAudit(t, r)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	enable, disable := entries[0], entries[1]
	if enable.Action != service.TokenAuditEnable || enable.Affected != 1 || enable.Actor != "admin" || len(enable.TokenIDs) != 1 {
		t.Fatalf("enable entry = %+v", enable)
	}
	// Token 2 was already disabled: requested 2, changed 1.
	if disable.Action != service.TokenAuditDisable || disable.Requested != 2 || disable.Affected != 1 || disable.At == 0 {
		t.Fatalf("disable entry = %+v", disable)
	}
}

func TestFailedBatchIsAuditedWithItsError(t *testing.T) {
	r := installTokenRouter(t)
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = "1"
	}
	rec := serveJSON(t, r, http.MethodPost, "/api/tokens/batch-disable", `{"ids":[`+strings.Join(ids, ",")+`]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("oversized batch: %d %s", rec.Code, rec.Body.String())
	}
	entries := readTokenAudit(t, r)
	if len(entries) != 1 || entries[0].Error == "" || entries[0].Affected != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestEmptyBatchIsRejectedWithoutAudit(t *testing.T) {
	r := installTokenRouter(t)
	if rec := serveJSON(t, r, http.MethodPost, "/api/tokens/batch-disable", `{"ids":[]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty batch: %d", rec.Code)
	}
	if entries := readTokenAudit(t, r); len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}
