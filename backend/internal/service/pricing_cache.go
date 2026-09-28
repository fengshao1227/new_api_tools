package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
)

// Caching for the cost-baseline page.
//
// Every build reads the gateway 5+ times. It fails fast when the gateway is
// unreachable — on 2026-09-24 the Tool pointed at a stopped blue/green colour
// and all 12 of the page's 5xx in 14 days were such fast failures — and slowly
// when the gateway is slow. So a build that fails serves the last good result,
// marked stale, instead of an error page; concurrent requests (a user
// clicking retry) share one build instead of each starting their own.

const (
	pricingCacheKey    = "margin-analysis:pricing:v2"
	pricingLastGoodKey = "margin-analysis:pricing:v2:last-good"
	pricingFreshTTL    = 10 * time.Minute
	pricingLastGoodTTL = 7 * 24 * time.Hour
)

// buildPricingAnalysis is a variable so tests can replace the gateway.
var buildPricingAnalysis = buildPricingAnalysisFromGateway

var pricingBuilds pricingFlight

// GetPricingAnalysis returns the cost-baseline report. refresh skips the
// fresh cache; it still falls back to the last good result on failure.
func GetPricingAnalysis(refresh bool) (*PricingAnalysisResult, error) {
	cm := cache.Get()
	if !refresh {
		var cached PricingAnalysisResult
		if found, _ := cm.GetJSON(pricingCacheKey, &cached); found {
			return &cached, nil
		}
	}
	result, err := pricingBuilds.do(func() (*PricingAnalysisResult, error) {
		built, buildErr := buildPricingAnalysis()
		if buildErr != nil {
			return nil, buildErr
		}
		built.GeneratedAt = time.Now().Unix()
		cm.Set(pricingCacheKey, built, pricingFreshTTL)
		cm.Set(pricingLastGoodKey, built, pricingLastGoodTTL)
		return built, nil
	})
	if err == nil {
		return result, nil
	}
	var last PricingAnalysisResult
	if found, _ := cm.GetJSON(pricingLastGoodKey, &last); found {
		last.Stale = true
		last.StaleReason = err.Error()
		return &last, nil
	}
	return nil, err
}

// IsPricingTimeout reports whether err is the gateway running out of time,
// as opposed to refusing or being unreachable.
func IsPricingTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// pricingFlight lets concurrent callers share one in-flight build.
type pricingFlight struct {
	mu   sync.Mutex
	call *pricingCall
}

type pricingCall struct {
	done   chan struct{}
	result *PricingAnalysisResult
	err    error
}

func (f *pricingFlight) do(build func() (*PricingAnalysisResult, error)) (*PricingAnalysisResult, error) {
	f.mu.Lock()
	if c := f.call; c != nil {
		f.mu.Unlock()
		<-c.done
		return c.result, c.err
	}
	c := &pricingCall{done: make(chan struct{})}
	f.call = c
	f.mu.Unlock()

	func() {
		// A panic must still release the waiters; they get it as an error.
		defer func() {
			if r := recover(); r != nil {
				c.result, c.err = nil, fmt.Errorf("成本基准计算异常: %v", r)
			}
		}()
		c.result, c.err = build()
	}()

	f.mu.Lock()
	f.call = nil
	f.mu.Unlock()
	close(c.done)
	return c.result, c.err
}
