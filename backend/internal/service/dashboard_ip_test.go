package service

import (
	"testing"
	"time"
)

func TestParsePeriodCoversEveryAcceptedWindow(t *testing.T) {
	for window, seconds := range WindowSeconds {
		start, end := parsePeriodToTimestamps(window)
		if end-start != seconds {
			t.Fatalf("window %s spans %d s, want %d", window, end-start, seconds)
		}
	}
	if start, end := parsePeriodToTimestamps("bogus"); end-start != 7*86400 {
		t.Fatalf("unknown window spans %d s, want 7 days", end-start)
	}
}

// TestIPDistributionIsCountryOnly: the response carries the country ranking
// and the sample description, and nothing China-centred.
func TestIPDistributionIsCountryOnly(t *testing.T) {
	installIPMonitoringSchema(t)
	stubGeoIP(t)
	clearIPTestCaches(t)

	db := NewDashboardService().db.DB
	now := time.Now().Unix()
	// Two private IPs (one country: 本地网络/LO) and one public IP the stub
	// GeoIP cannot place (未知/XX).
	for _, row := range []struct {
		user int
		ip   string
	}{{1, "10.0.0.1"}, {1, "10.0.0.1"}, {2, "10.0.0.2"}, {3, "203.0.113.9"}} {
		if _, err := db.Exec(`INSERT INTO logs (user_id, created_at, type, ip) VALUES (?, ?, 2, ?)`, row.user, now, row.ip); err != nil {
			t.Fatal(err)
		}
	}

	res, err := NewDashboardService().GetIPDistribution("24h", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"domestic_percentage", "overseas_percentage", "by_province", "top_cities"} {
		if _, ok := res[removed]; ok {
			t.Fatalf("response still has %q", removed)
		}
	}
	for _, kept := range []string{"total_ips", "total_requests", "sampled_ip_limit", "sampled_ips", "sampled_requests", "coverage_percentage", "geo_available", "snapshot_time"} {
		if _, ok := res[kept]; !ok {
			t.Fatalf("response lost sample field %q", kept)
		}
	}
	countries := res["by_country"].([]map[string]interface{})
	if len(countries) != 2 {
		t.Fatalf("countries = %+v, want LO and XX", countries)
	}
	top := countries[0]
	if top["country_code"] != "LO" || toInt64(top["ip_count"]) != 2 || toInt64(top["request_count"]) != 3 || toInt64(top["user_count"]) != 2 || toFloat64(top["percentage"]) != 75 {
		t.Fatalf("top country = %+v", top)
	}
	if countries[1]["country_code"] != "XX" || countries[1]["country"] != "未知" {
		t.Fatalf("unplaced IP row = %+v", countries[1])
	}
}
