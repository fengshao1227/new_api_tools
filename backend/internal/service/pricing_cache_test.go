package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
)

// stubPricingBuild replaces the gateway build and clears the report caches.
func stubPricingBuild(t *testing.T, build func() (*PricingAnalysisResult, error)) {
	t.Helper()
	cm := cache.Get()
	clear := func() {
		_ = cm.Delete(pricingCacheKey)
		_ = cm.Delete(pricingLastGoodKey)
	}
	clear()
	saved := buildPricingAnalysis
	buildPricingAnalysis = build
	t.Cleanup(func() {
		buildPricingAnalysis = saved
		clear()
	})
}

func TestGetPricingAnalysisServesLastGoodResultWhenRefreshFails(t *testing.T) {
	fail := false
	stubPricingBuild(t, func() (*PricingAnalysisResult, error) {
		if fail {
			return nil, errors.New("dial tcp: lookup beat-new-api-green: no such host")
		}
		return &PricingAnalysisResult{Currency: "USD", Notes: []string{"ok"}}, nil
	})

	first, err := GetPricingAnalysis(false)
	if err != nil || first.Stale || first.GeneratedAt == 0 {
		t.Fatalf("first build = %+v, %v", first, err)
	}
	fail = true
	cached, err := GetPricingAnalysis(false)
	if err != nil || cached.Stale {
		t.Fatalf("fresh cache should answer without building: %+v, %v", cached, err)
	}
	stale, err := GetPricingAnalysis(true)
	if err != nil {
		t.Fatalf("refresh failure with a last good result must not be an error: %v", err)
	}
	if !stale.Stale || stale.StaleReason == "" || stale.GeneratedAt != first.GeneratedAt || stale.Notes[0] != "ok" {
		t.Fatalf("stale result = %+v", stale)
	}
}

func TestGetPricingAnalysisReportsErrorWithNothingToFallBackOn(t *testing.T) {
	stubPricingBuild(t, func() (*PricingAnalysisResult, error) {
		return nil, errors.New("new-api 定价接口返回 HTTP 500")
	})
	if result, err := GetPricingAnalysis(false); err == nil || result != nil {
		t.Fatalf("got %+v, %v; want an error", result, err)
	}
}

func TestPricingFlightSharesOneBuildBetweenConcurrentCallers(t *testing.T) {
	var builds int32
	release := make(chan struct{})
	var flight pricingFlight
	var wg sync.WaitGroup
	results := make([]*PricingAnalysisResult, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = flight.do(func() (*PricingAnalysisResult, error) {
				atomic.AddInt32(&builds, 1)
				<-release
				return &PricingAnalysisResult{Currency: "USD"}, nil
			})
		}(i)
	}
	// Let every caller queue behind the first build, then finish it.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if builds != 1 {
		t.Fatalf("builds = %d, want 1", builds)
	}
	for i, r := range results {
		if r == nil {
			t.Fatalf("caller %d got no result", i)
		}
	}
	// The flight is released: the next call builds again.
	if _, err := flight.do(func() (*PricingAnalysisResult, error) { return nil, errors.New("second") }); err == nil || err.Error() != "second" {
		t.Fatalf("second build did not run: %v", err)
	}
}

func TestPricingFlightTurnsPanicIntoError(t *testing.T) {
	var flight pricingFlight
	if _, err := flight.do(func() (*PricingAnalysisResult, error) { panic("boom") }); err == nil {
		t.Fatal("panic was not reported")
	}
}

// TestPricingClientGivesUpWithinItsBudget: a gateway slower than the request
// timeout yields a timeout error — the handler's 504 — well before the
// server's 60 s write timeout, instead of a response nginx never receives.
func TestPricingClientGivesUpWithinItsBudget(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	client := &pricingHTTPClient{gateway: newAPIAdminTarget{baseURL: slow.URL, apiKey: "k"}, client: &http.Client{Timeout: 100 * time.Millisecond}}

	started := time.Now()
	var envelope costBaselineEnvelope
	err := client.getJSON(context.Background(), "/api/data/cost-baseline", url.Values{}, &envelope)
	if err == nil || !IsPricingTimeout(err) {
		t.Fatalf("slow gateway error = %v, want a timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("gave up after %s, want about 100ms", elapsed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	client.client = &http.Client{Timeout: time.Minute}
	if err := client.getJSON(ctx, "/api/data/cost-baseline", url.Values{}, &envelope); !IsPricingTimeout(err) {
		t.Fatalf("build budget expiry = %v, want a timeout", err)
	}
}

func TestPricingClientRefusalIsNotATimeout(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer broken.Close()
	client := &pricingHTTPClient{gateway: newAPIAdminTarget{baseURL: broken.URL, apiKey: "k"}, client: broken.Client()}
	var envelope costBaselineEnvelope
	err := client.getJSON(context.Background(), "/api/data/cost-baseline", url.Values{}, &envelope)
	if err == nil || IsPricingTimeout(err) {
		t.Fatalf("HTTP 500 = %v, want a non-timeout error", err)
	}
}

func TestPricingBudgetStaysUnderServerWriteTimeout(t *testing.T) {
	// cmd/server/main.go sets WriteTimeout to 60 s. Past it the response is
	// dropped and nginx logs a bare 502.
	const serverWriteTimeout = 60 * time.Second
	if pricingBuildBudget >= serverWriteTimeout-5*time.Second || pricingRequestTimeout > pricingBuildBudget {
		t.Fatalf("budget %s / request %s must leave headroom under the %s write timeout", pricingBuildBudget, pricingRequestTimeout, serverWriteTimeout)
	}
}
