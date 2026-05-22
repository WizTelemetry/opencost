package costmodel

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/stretchr/testify/assert"
)

func TestCacheTTLForWindow(t *testing.T) {
	now := time.Now()
	historicalEnd := now.Add(-2 * time.Hour)
	realtimeEnd := now.Add(-30 * time.Minute)
	futureEnd := now.Add(time.Hour)

	tests := []struct {
		name     string
		window   *opencost.Window
		expected time.Duration
	}{
		{name: "nil window returns default expiration", window: nil, expected: 0},
		{
			name: "nil end returns default expiration",
			window: func() *opencost.Window {
				w := opencost.NewWindow(&historicalEnd, nil)
				return &w
			}(),
			expected: 0,
		},
		{
			name: "historical window returns at least 5m",
			window: func() *opencost.Window {
				w := opencost.NewWindow(&historicalEnd, &historicalEnd)
				return &w
			}(),
			expected: 5 * time.Minute,
		},
		{
			name: "near realtime caps at 30s",
			window: func() *opencost.Window {
				w := opencost.NewWindow(&realtimeEnd, &realtimeEnd)
				return &w
			}(),
			expected: 30 * time.Second,
		},
		{
			name: "future end is treated as realtime",
			window: func() *opencost.Window {
				w := opencost.NewWindow(&futureEnd, &futureEnd)
				return &w
			}(),
			expected: 30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, cacheTTLForWindow(tt.window))
		})
	}
}

func TestCacheTTLForWindowRespectsGlobalConfig(t *testing.T) {
	now := time.Now()
	historicalEnd := now.Add(-2 * time.Hour)
	realtimeEnd := now.Add(-30 * time.Minute)
	wHistorical := opencost.NewWindow(&historicalEnd, &historicalEnd)
	wRealtime := opencost.NewWindow(&realtimeEnd, &realtimeEnd)

	t.Run("historical uses longer configured ttl", func(t *testing.T) {
		t.Setenv(queryCacheTTLEnvVar, "600")
		assert.Equal(t, 10*time.Minute, cacheTTLForWindow(&wHistorical))
	})

	t.Run("realtime uses shorter configured ttl", func(t *testing.T) {
		t.Setenv(queryCacheTTLEnvVar, "10")
		assert.Equal(t, 10*time.Second, cacheTTLForWindow(&wRealtime))
	})

	t.Run("realtime caps long configured ttl at 30s", func(t *testing.T) {
		t.Setenv(queryCacheTTLEnvVar, "600")
		assert.Equal(t, 30*time.Second, cacheTTLForWindow(&wRealtime))
	})

	t.Run("historical floors short configured ttl at 5m", func(t *testing.T) {
		t.Setenv(queryCacheTTLEnvVar, "30")
		assert.Equal(t, 5*time.Minute, cacheTTLForWindow(&wHistorical))
	})
}

func TestCacheTTLForWindow_OverrideEnvVars(t *testing.T) {
	now := time.Now()
	historicalEnd := now.Add(-2 * time.Hour)
	realtimeEnd := now.Add(-30 * time.Minute)
	wHistorical := opencost.NewWindow(&historicalEnd, &historicalEnd)
	wRealtime := opencost.NewWindow(&realtimeEnd, &realtimeEnd)

	t.Run("realtime override takes precedence over baseline", func(t *testing.T) {
		t.Setenv(queryCacheTTLEnvVar, "600")
		t.Setenv(realtimeQueryCacheTTLEnvVar, "120")
		assert.Equal(t, 2*time.Minute, cacheTTLForWindow(&wRealtime))
	})

	t.Run("historical override takes precedence over baseline", func(t *testing.T) {
		t.Setenv(queryCacheTTLEnvVar, "10")
		t.Setenv(historicalQueryCacheTTLEnvVar, "300")
		assert.Equal(t, 5*time.Minute, cacheTTLForWindow(&wHistorical))
	})

	t.Run("zero realtime disables cache for realtime", func(t *testing.T) {
		t.Setenv(realtimeQueryCacheTTLEnvVar, "0")
		assert.Equal(t, time.Duration(0), cacheTTLForWindow(&wRealtime))
	})

	t.Run("zero historical disables cache for historical", func(t *testing.T) {
		t.Setenv(historicalQueryCacheTTLEnvVar, "0")
		assert.Equal(t, time.Duration(0), cacheTTLForWindow(&wHistorical))
	})
}

func TestQueryCacheKey_NormalizesRelativeWindow(t *testing.T) {
	req1 := httptest.NewRequest(http.MethodGet, "/allocation?window=7d&aggregate=namespace", nil)
	req2 := httptest.NewRequest(http.MethodGet, "/allocation?window=7d&aggregate=namespace", nil)

	// Within the same 5-minute bucket, keys should be identical
	key1 := queryCacheKey("allocation", req1)
	key2 := queryCacheKey("allocation", req2)
	assert.Equal(t, key1, key2, "identical relative windows within same bucket should produce same key")

	// Different window values produce different keys
	req3 := httptest.NewRequest(http.MethodGet, "/allocation?window=24h&aggregate=namespace", nil)
	key3 := queryCacheKey("allocation", req3)
	assert.NotEqual(t, key1, key3, "different relative windows should produce different keys")
}

func TestQueryCacheKey_CanonicalizesPrefixedPaths(t *testing.T) {
	legacyReq := httptest.NewRequest(http.MethodGet, "/allocation?window=7d&aggregate=namespace", nil)
	prefixedReq := httptest.NewRequest(http.MethodGet, RoutePrefix+"/allocation?window=7d&aggregate=namespace", nil)

	assert.Equal(t, queryCacheKey("allocation", legacyReq), queryCacheKey("allocation", prefixedReq))
}

func TestQueryCacheKey_CanonicalizesAllocationAliases(t *testing.T) {
	allocationReq := httptest.NewRequest(http.MethodGet, RoutePrefix+"/allocation?window=7d&aggregate=namespace", nil)
	computeReq := httptest.NewRequest(http.MethodGet, RoutePrefix+"/allocation/compute?window=7d&aggregate=namespace", nil)
	summaryReq := httptest.NewRequest(http.MethodGet, RoutePrefix+"/allocation/summary?window=7d&aggregate=namespace", nil)
	computeSummaryReq := httptest.NewRequest(http.MethodGet, RoutePrefix+"/allocation/compute/summary?window=7d&aggregate=namespace", nil)

	assert.Equal(t, queryCacheKey("allocation", allocationReq), queryCacheKey("allocation", computeReq))
	assert.Equal(t, queryCacheKey("allocation-summary", summaryReq), queryCacheKey("allocation-summary", computeSummaryReq))
}

func TestQueryCacheKey_PreservesAbsoluteWindows(t *testing.T) {
	req1 := httptest.NewRequest(http.MethodGet, "/allocation?window=2025-05-01T00:00:00Z,2025-05-08T00:00:00Z&aggregate=namespace", nil)
	req2 := httptest.NewRequest(http.MethodGet, "/allocation?window=2025-05-01T00:00:00Z,2025-05-08T00:00:00Z&aggregate=namespace", nil)

	// Absolute windows should NOT be normalized — keys should be identical
	key1 := queryCacheKey("allocation", req1)
	key2 := queryCacheKey("allocation", req2)
	assert.Equal(t, key1, key2, "absolute timestamps should produce identical keys")
	assert.NotContains(t, key1, "__normalized_at", "absolute windows should not be normalized")
}

func TestNormalizeCacheWindow(t *testing.T) {
	t.Run("empty query returns empty", func(t *testing.T) {
		assert.Equal(t, "", normalizeCacheWindow(""))
	})

	t.Run("no window parameter returns unchanged", func(t *testing.T) {
		q := "aggregate=namespace&accumulate=true"
		assert.Equal(t, q, normalizeCacheWindow(q))
	})

	t.Run("relative window adds normalized_at", func(t *testing.T) {
		result := normalizeCacheWindow("window=7d&aggregate=namespace")
		assert.Contains(t, result, "__normalized_at=")
		assert.Contains(t, result, "window=7d")
	})

	t.Run("absolute window with comma preserved", func(t *testing.T) {
		q := "window=2025-05-01T00:00:00Z,2025-05-08T00:00:00Z"
		assert.Equal(t, q, normalizeCacheWindow(q))
	})

	t.Run("absolute window with T preserved", func(t *testing.T) {
		q := "window=2025-05-01T00:00:00Z"
		assert.Equal(t, q, normalizeCacheWindow(q))
	})
}
