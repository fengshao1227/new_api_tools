package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Token batch enable/disable audit trail. The Tool writes tokens straight into
// the gateway database, and the gateway keeps no record of it, so every batch
// is appended here — one JSON object per line in the Tool's data directory,
// next to panel_whitelist.json (the /app/data volume in the container).

// Token audit actions.
const (
	TokenAuditDisable = "disable"
	TokenAuditEnable  = "enable"
)

// TokenAuditEntry is one batch operation as the Tool performed it.
type TokenAuditEntry struct {
	At int64 `json:"at"`
	// Actor is the Tool login identity: the JWT subject ("admin" for the
	// password login) or "api_key" for X-API-Key calls.
	Actor     string  `json:"actor"`
	IP        string  `json:"ip,omitempty"`
	Action    string  `json:"action"`
	TokenIDs  []int64 `json:"token_ids"`
	Requested int     `json:"requested"`
	Affected  int64   `json:"affected"`
	Error     string  `json:"error,omitempty"`
}

const (
	tokenAuditFileName = "token_audit.jsonl"
	// Past maxBytes the file is rewritten with its newest keepEntries lines.
	tokenAuditMaxBytes    = 2 << 20
	tokenAuditKeepEntries = 2000
	tokenAuditMaxList     = 200
)

type tokenAuditStore struct {
	mu   sync.Mutex
	path string
}

var globalTokenAudit = &tokenAuditStore{path: defaultDataFilePath(tokenAuditFileName)}

// RecordTokenAudit appends one entry.
func RecordTokenAudit(entry TokenAuditEntry) error {
	return globalTokenAudit.append(entry)
}

// RecentTokenAudit returns up to limit entries, newest first.
func RecentTokenAudit(limit int) ([]TokenAuditEntry, error) {
	if limit <= 0 || limit > tokenAuditMaxList {
		limit = 20
	}
	return globalTokenAudit.recent(limit)
}

// SetTokenAuditPathForTest points the store at path and returns the restore.
func SetTokenAuditPathForTest(path string) func() {
	globalTokenAudit.mu.Lock()
	previous := globalTokenAudit.path
	globalTokenAudit.path = path
	globalTokenAudit.mu.Unlock()
	return func() {
		globalTokenAudit.mu.Lock()
		globalTokenAudit.path = previous
		globalTokenAudit.mu.Unlock()
	}
}

func (s *tokenAuditStore) append(entry TokenAuditEntry) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(line, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if info, err := os.Stat(s.path); err == nil && info.Size() > tokenAuditMaxBytes {
		return s.compactLocked()
	}
	return nil
}

// compactLocked rewrites the file with its newest lines; s.mu must be held.
func (s *tokenAuditStore) compactLocked() error {
	lines, err := s.readLinesLocked()
	if err != nil {
		return err
	}
	if len(lines) > tokenAuditKeepEntries {
		lines = lines[len(lines)-tokenAuditKeepEntries:]
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(bytes.Join(lines, []byte("\n")), '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *tokenAuditStore) readLinesLocked() ([][]byte, error) {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines [][]byte
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
			lines = append(lines, append([]byte(nil), line...))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read token audit: %w", err)
	}
	return lines, nil
}

func (s *tokenAuditStore) recent(limit int) ([]TokenAuditEntry, error) {
	s.mu.Lock()
	lines, err := s.readLinesLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	entries := make([]TokenAuditEntry, 0, limit)
	for i := len(lines) - 1; i >= 0 && len(entries) < limit; i-- {
		var entry TokenAuditEntry
		if json.Unmarshal(lines[i], &entry) == nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}
