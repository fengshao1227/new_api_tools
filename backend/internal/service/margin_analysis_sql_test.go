package service

import (
	"strings"
	"testing"

	"github.com/new-api-tools/backend/internal/database"
)

func TestMarginJSONFieldConditionPostgresDoesNotCastHistoricalText(t *testing.T) {
	db := &database.Manager{IsPG: true}

	unpriced := marginJSONFieldCondition(db, "unpriced", "true")
	if strings.Contains(unpriced, "::jsonb") || !strings.Contains(unpriced, "other ~") {
		t.Fatalf("unpriced condition must avoid PostgreSQL JSON casts: %s", unpriced)
	}
	if !strings.Contains(unpriced, `"unpriced"`) || !strings.Contains(unpriced, "true") {
		t.Fatalf("unpriced condition lost its field/value match: %s", unpriced)
	}

	estimated := marginJSONFieldCondition(db, "cost_source", "estimated")
	if strings.Contains(estimated, "::jsonb") || !strings.Contains(estimated, `"cost_source"`) || !strings.Contains(estimated, `"estimated"`) {
		t.Fatalf("estimated condition must use a safe text match: %s", estimated)
	}
}
