package costmodel

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
)

func TestAssetsGraphCacheHit(t *testing.T) {
	queryCache := cache.New(5*time.Minute, 10*time.Minute)
	a := &Accesses{QueryCache: queryCache}

	// Pre-populate cache
	req := httptest.NewRequest(http.MethodGet, "/assets/graph?window=7d&aggregate=type", nil)
	expectedResp := []byte(`{"code":200,"data":null}`)
	a.setQueryCacheResponseWithTTL("assets-graph", req, expectedResp, 5*time.Minute)

	// Handler should return cached response
	rr := httptest.NewRecorder()
	a.ComputeAssetsGraphHandler(rr, req, httprouter.Params{})

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, expectedResp, rr.Body.Bytes())
}

func TestAssetsGraphCacheMiss(t *testing.T) {
	queryCache := cache.New(5*time.Minute, 10*time.Minute)
	a := &Accesses{QueryCache: queryCache}

	req := httptest.NewRequest(http.MethodGet, "/assets/graph?window=7d&aggregate=type", nil)

	// Handler should not find cache and fall through.
	// Without a model, the handler will panic (nil pointer dereference).
	// Recover and verify cache was NOT written.
	rr := httptest.NewRecorder()
	func() {
		defer func() { _ = recover() }()
		a.ComputeAssetsGraphHandler(rr, req, httprouter.Params{})
	}()

	_, found := queryCache.Get(queryCacheKey("assets-graph", req))
	assert.False(t, found, "cache should not be written for error responses")
}

func TestAssetsGraphCacheKeyVariesWithParams(t *testing.T) {
	queryCache := cache.New(5*time.Minute, 10*time.Minute)
	_ = &Accesses{QueryCache: queryCache}

	req1 := httptest.NewRequest(http.MethodGet, "/assets/graph?window=7d&aggregate=type", nil)
	req2 := httptest.NewRequest(http.MethodGet, "/assets/graph?window=7d&aggregate=name", nil)
	req3 := httptest.NewRequest(http.MethodGet, "/assets/graph?window=7d&aggregate=type", nil)

	key1 := queryCacheKey("assets-graph", req1)
	key2 := queryCacheKey("assets-graph", req2)
	key3 := queryCacheKey("assets-graph", req3)

	assert.NotEqual(t, key1, key2, "different params should produce different cache keys")
	assert.Equal(t, key1, key3, "same params should produce same cache key")
}
