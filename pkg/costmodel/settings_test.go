package costmodel

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
)

func TestFlushCostDataCachesClearsResponseAndStepCaches(t *testing.T) {
	queryCache := cache.New(time.Minute, time.Minute)
	allocationCache := cache.New(time.Minute, time.Minute)
	assetCache := cache.New(time.Minute, time.Minute)
	queryCache.Set("response", "stale", cache.DefaultExpiration)
	allocationCache.Set("allocation", "stale", cache.DefaultExpiration)
	assetCache.Set("asset", "stale", cache.DefaultExpiration)

	accesses := &Accesses{
		QueryCache: queryCache,
		Model: &CostModel{
			allocStepCache: allocationCache,
			assetStepCache: assetCache,
		},
	}
	accesses.flushCostDataCaches("test pricing change")

	assert.Equal(t, 0, queryCache.ItemCount())
	assert.Equal(t, 0, allocationCache.ItemCount())
	assert.Equal(t, 0, assetCache.ItemCount())
	assert.NotEqual(t, accesses.Model.stepCacheKey("step"), "step")
}

func TestPricingCacheEpochKeepsInflightResponseOutOfNewGeneration(t *testing.T) {
	queryCache := cache.New(time.Minute, time.Minute)
	accesses := &Accesses{QueryCache: queryCache}
	oldRequest := accesses.withQueryCacheEpoch(httptest.NewRequest("GET", "/allocation?window=7d", nil))

	accesses.flushCostDataCaches("test pricing change")

	// Simulate an old request finishing after the flush. It may only write its
	// old generation key, which new requests cannot observe.
	accesses.setQueryCacheResponseWithTTL("allocation", oldRequest, []byte("old"), time.Minute)
	newRequest := accesses.withQueryCacheEpoch(httptest.NewRequest("GET", "/allocation?window=7d", nil))
	_, found := accesses.getQueryCacheResponse("allocation", newRequest)
	assert.False(t, found)
}
