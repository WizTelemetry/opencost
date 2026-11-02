package costmodel

import (
	"testing"
	"time"

	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetStepCacheKey(t *testing.T) {
	start := time.Unix(1000, 0)
	end := time.Unix(2000, 0)

	key := assetStepCacheKey(start, end, time.Minute)
	assert.Equal(t, key, assetStepCacheKey(start, end, time.Minute))
	assert.NotEqual(t, key, assetStepCacheKey(start.Add(time.Second), end, time.Minute))
	assert.NotEqual(t, key, assetStepCacheKey(start, end, 2*time.Minute))
}

func TestAssetStepCacheTTLOverrides(t *testing.T) {
	now := time.Now()

	t.Run("realtime override", func(t *testing.T) {
		t.Setenv(assetStepRealtimeTTLEnvVar, "120")
		assert.Equal(t, 2*time.Minute, assetStepCacheTTL(now.Add(-30*time.Minute)))
	})

	t.Run("historical override", func(t *testing.T) {
		t.Setenv(assetStepHistoricalTTLEnvVar, "1800")
		assert.Equal(t, 30*time.Minute, assetStepCacheTTL(now.Add(-2*time.Hour)))
	})

	t.Run("zero disables realtime cache", func(t *testing.T) {
		t.Setenv(assetStepRealtimeTTLEnvVar, "0")
		assert.Equal(t, time.Duration(0), assetStepCacheTTL(now.Add(-30*time.Minute)))
	})
}

func TestComputeAssetsCacheHitReturnsClone(t *testing.T) {
	start := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
	window := opencost.NewClosedWindow(start, end)

	node := opencost.NewNode("node-a", "cluster-a", "provider-a", start, end, window)
	node.CPUCost = 3
	original := opencost.NewAssetSet(start, end, node)

	stepCache := cache.New(5*time.Minute, 10*time.Minute)
	stepCache.Set(assetStepCacheKey(start, end, 0), &assetStepCacheValue{
		assetSet: original.Clone(),
	}, cache.DefaultExpiration)

	cm := &CostModel{assetStepCache: stepCache}
	assetSet, err := cm.ComputeAssets(start, end)
	require.NoError(t, err)

	extra := opencost.NewNode("node-b", "cluster-a", "provider-b", start, end, window)
	require.NoError(t, assetSet.Insert(extra, nil))

	assetSetAgain, err := cm.ComputeAssets(start, end)
	require.NoError(t, err)
	assert.Equal(t, 1, assetSetAgain.Length())
}
