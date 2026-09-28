package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenAuditKeepsNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", tokenAuditFileName)
	t.Cleanup(SetTokenAuditPathForTest(path))

	if entries, err := RecentTokenAudit(10); err != nil || len(entries) != 0 {
		t.Fatalf("empty store = %v, %v", entries, err)
	}
	for i, action := range []string{TokenAuditDisable, TokenAuditEnable, TokenAuditDisable} {
		entry := TokenAuditEntry{At: int64(100 + i), Actor: "admin", Action: action, TokenIDs: []int64{int64(i), 42}, Requested: 2, Affected: 1}
		if err := RecordTokenAudit(entry); err != nil {
			t.Fatal(err)
		}
	}
	// A torn or foreign line must not hide the rest.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()

	entries, err := RecentTokenAudit(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].At != 102 || entries[1].At != 101 || entries[1].Action != TokenAuditEnable {
		t.Fatalf("recent = %+v", entries)
	}
	if entries[0].TokenIDs[0] != 2 || entries[0].Actor != "admin" {
		t.Fatalf("entry fields lost: %+v", entries[0])
	}
}

func TestTokenAuditCompactionKeepsNewestEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), tokenAuditFileName)
	t.Cleanup(SetTokenAuditPathForTest(path))
	for i := 0; i < tokenAuditKeepEntries+5; i++ {
		if err := RecordTokenAudit(TokenAuditEntry{At: int64(i), Action: TokenAuditDisable, TokenIDs: []int64{1}}); err != nil {
			t.Fatal(err)
		}
	}
	globalTokenAudit.mu.Lock()
	err := globalTokenAudit.compactLocked()
	lines, readErr := globalTokenAudit.readLinesLocked()
	globalTokenAudit.mu.Unlock()
	if err != nil || readErr != nil {
		t.Fatalf("compact: %v / %v", err, readErr)
	}
	if len(lines) != tokenAuditKeepEntries {
		t.Fatalf("kept %d lines", len(lines))
	}
	entries, _ := RecentTokenAudit(1)
	if len(entries) != 1 || entries[0].At != int64(tokenAuditKeepEntries+4) {
		t.Fatalf("newest after compaction = %+v", entries)
	}
}
