package util

import (
	"fmt"
	"time"
)

// ParseDateToTimestamp parses a date string to Unix timestamp
// Supports ISO 8601 (2024-01-01T00:00:00Z) and date-only (2024-01-01)
func parseDateToTimestamp(dateStr string, endOfDay bool) (int64, error) {
	// Try ISO 8601 with timezone
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}

	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, dateStr, time.Local)
		if err == nil {
			if endOfDay && layout == "2006-01-02" {
				t = t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
			}
			return t.Unix(), nil
		}
	}

	return 0, fmt.Errorf("invalid date format: %s", dateStr)
}

// ParseDateToTimestampPublic is the exported version for use outside util
func ParseDateToTimestampPublic(dateStr string, endOfDay bool) (int64, error) {
	return parseDateToTimestamp(dateStr, endOfDay)
}
