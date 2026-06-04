package costmodel

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/util/interval"
)

const (
	cacheWarmerEnabledEnvVar      = "OPENCOST_CACHE_WARMER_ENABLED"
	cacheWarmerIntervalEnvVar     = "OPENCOST_CACHE_WARMER_INTERVAL_SECONDS"
	cacheWarmerStartupDelayEnvVar = "OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS"
	cacheWarmerEndpointsEnvVar    = "OPENCOST_CACHE_WARMER_ENDPOINTS"

	defaultCacheWarmerInterval     = 5 * time.Minute
	defaultCacheWarmerStartupDelay = 30 * time.Second
)

// CacheWarmer periodically pre-computes configured API endpoints to warm the query cache.
type CacheWarmer struct {
	accesses     *Accesses
	runner       *interval.IntervalRunner
	enabled      bool
	interval     time.Duration
	startupDelay time.Duration
	endpoints    []string
	mu           sync.Mutex
}

// NewCacheWarmer creates a new cache warmer for the given Accesses instance.
func NewCacheWarmer(a *Accesses) *CacheWarmer {
	enabled := os.Getenv(cacheWarmerEnabledEnvVar) == "true"
	if !enabled {
		return &CacheWarmer{enabled: false}
	}

	interval := durationFromEnv(cacheWarmerIntervalEnvVar, defaultCacheWarmerInterval)
	startupDelay := durationFromEnv(cacheWarmerStartupDelayEnvVar, defaultCacheWarmerStartupDelay)
	endpoints := parseEndpoints(os.Getenv(cacheWarmerEndpointsEnvVar))

	if len(endpoints) == 0 {
		endpoints = []string{
			"/allocation?window=7d&aggregate=namespace&accumulate=true&includeIdle=true",
			"/allocation/summary?window=7d&aggregate=namespace&accumulate=true",
			"/allocation/summary/topline?window=7d&aggregate=namespace&accumulate=true",
			"/assets?window=7d&aggregate=type&accumulate=true",
			"/assets/graph?window=7d&aggregate=type",
			"/efficiency?window=7d&aggregate=namespace",
			"/efficiency/clusters?window=7d&step=1d&accumulate=true",
			"/efficiency/clusters/summary?window=7d&step=1d&accumulate=true",
		}
	}

	return &CacheWarmer{
		accesses:     a,
		enabled:      true,
		interval:     interval,
		startupDelay: startupDelay,
		endpoints:    endpoints,
	}
}

// Start begins the cache warmer. Returns true if started, false if already running or disabled.
func (cw *CacheWarmer) Start() bool {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if !cw.enabled || cw.runner != nil {
		return false
	}

	time.AfterFunc(cw.startupDelay, func() {
		cw.warmAll()
	})

	cw.runner = interval.NewIntervalRunner(cw.warmAll, cw.interval)
	return cw.runner.Start()
}

// Stop stops the cache warmer.
func (cw *CacheWarmer) Stop() bool {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if cw.runner == nil {
		return false
	}
	stopped := cw.runner.Stop()
	cw.runner = nil
	return stopped
}

// warmAll iterates all configured endpoints and warms each one.
func (cw *CacheWarmer) warmAll() {
	for _, endpoint := range cw.endpoints {
		cw.warmEndpoint(endpoint)
	}
}

// warmEndpoint warms a single endpoint by calling the handler directly.
func (cw *CacheWarmer) warmEndpoint(endpoint string) {
	start := time.Now()

	cw.mu.Lock()
	a := cw.accesses
	cw.mu.Unlock()

	if a == nil {
		log.Warnf("CacheWarmer: no Accesses instance available, skipping %s", endpoint)
		return
	}

	// Parse the endpoint into path and query string
	parts := strings.SplitN(endpoint, "?", 2)
	path := parts[0]
	rawQuery := ""
	if len(parts) == 2 {
		rawQuery = parts[1]
	}

	// Build a synthetic request
	targetURL := &url.URL{
		Path:     path,
		RawQuery: rawQuery,
	}
	req := httptest.NewRequest(http.MethodGet, targetURL.String(), nil)
	rr := httptest.NewRecorder()

	// Dispatch to the appropriate handler
	handler := a.warmHandlerForPath(path)
	if handler == nil {
		log.Warnf("CacheWarmer: no handler for path %s", path)
		return
	}

	handler(rr, req, httprouter.Params{})

	duration := time.Since(start)
	status := rr.Code
	log.Infof("CacheWarmer: warmed %s status=%d duration=%v", endpoint, status, duration)
}

// warmHandlerForPath returns the handler for the given path prefix.
func (a *Accesses) warmHandlerForPath(path string) httprouter.Handle {
	path = canonicalCachePath(path)

	switch {
	case strings.HasPrefix(path, "/allocation/summary/topline"):
		return a.ComputeAllocationHandlerSummaryTopline
	case strings.HasPrefix(path, "/allocation/summary"):
		return a.ComputeAllocationHandlerSummary
	case strings.HasPrefix(path, "/allocation"):
		return a.ComputeAllocationHandler
	case strings.HasPrefix(path, "/efficiency/clusters/summary"):
		return a.ComputeAllocationHandlerClusterEfficiencySummary
	case strings.HasPrefix(path, "/efficiency/clusters"):
		return a.ComputeAllocationHandlerClusterEfficiencySummary
	case strings.HasPrefix(path, "/efficiency"):
		return a.ComputeEfficiencyHandler
	case strings.HasPrefix(path, "/assets/graph"):
		return a.ComputeAssetsGraphHandler
	case strings.HasPrefix(path, "/assets"):
		return a.ComputeAssetsHandler
	default:
		return nil
	}
}

func durationFromEnv(name string, defaultVal time.Duration) time.Duration {
	s := os.Getenv(name)
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s + "s")
	if err != nil {
		log.Warnf("CacheWarmer: invalid duration env %s=%s, using default %v", name, s, defaultVal)
		return defaultVal
	}
	return d
}

func parseEndpoints(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	var endpoints []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		upper := strings.ToUpper(p)
		if strings.HasPrefix(upper, "POST") ||
			strings.HasPrefix(upper, "PUT") ||
			strings.HasPrefix(upper, "DELETE") ||
			strings.HasPrefix(upper, "PATCH") {
			log.Warnf("CacheWarmer: skipping potentially mutating endpoint: %s", p)
			continue
		}
		// Strip HTTP method prefix if present (e.g. "GET /allocation?...")
		if strings.HasPrefix(upper, "GET ") {
			p = strings.TrimSpace(p[3:])
		}
		endpoints = append(endpoints, p)
	}
	return endpoints
}
