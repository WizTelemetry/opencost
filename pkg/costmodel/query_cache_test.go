package costmodel

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cachedAllocationResult struct {
	value *allocStepCacheValue
	err   error
}

func TestComputeCachedStepAllocationSingleflightReturnsClones(t *testing.T) {
	start := time.Now().Add(-time.Hour).Truncate(time.Minute)
	end := start.Add(time.Hour)

	cm := &CostModel{allocStepCache: cache.New(5*time.Minute, 10*time.Minute)}
	var calls atomic.Int32
	compute := func() (*allocStepCacheValue, error) {
		calls.Add(1)
		time.Sleep(150 * time.Millisecond)
		allocSet := opencost.NewAllocationSet(start, end)
		allocSet.Set(&opencost.Allocation{
			Name:         "alloc-a",
			Properties:   &opencost.AllocationProperties{Pod: "pod-a", Namespace: "ns"},
			CPUCoreHours: 1,
		})
		return &allocStepCacheValue{
			allocSet: allocSet,
			nodeMap: map[nodeKey]*nodePricing{
				{Cluster: "cluster-a", Node: "node-a"}: {NodeType: "m5.large"},
			},
		}, nil
	}
	clone := func(value *allocStepCacheValue) *allocStepCacheValue {
		return &allocStepCacheValue{
			allocSet: value.allocSet.Clone(),
			nodeMap:  cloneNodeMap(value.nodeMap),
		}
	}

	const callers = 12
	startGate := make(chan struct{})
	results := make(chan cachedAllocationResult, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			value, err := computeCachedStep(
				cm,
				allocationStepCacheLayer,
				allocStepCacheKey(start, end, 0),
				cm.allocStepCache,
				5*time.Minute,
				compute,
				clone,
			)
			results <- cachedAllocationResult{value: value, err: err}
		}()
	}

	close(startGate)
	wg.Wait()
	close(results)

	assert.Equal(t, int32(1), calls.Load(), "concurrent cache misses should share one computation")

	values := make([]*allocStepCacheValue, 0, callers)
	for result := range results {
		require.NoError(t, result.err)
		values = append(values, result.value)
	}
	require.Len(t, values, callers)

	values[0].allocSet.Get("alloc-a").CPUCoreHours = 99
	values[0].nodeMap[nodeKey{Cluster: "cluster-a", Node: "node-a"}].NodeType = "modified"
	assert.Equal(t, 1.0, values[1].allocSet.Get("alloc-a").CPUCoreHours)
	assert.Equal(t, "m5.large", values[1].nodeMap[nodeKey{Cluster: "cluster-a", Node: "node-a"}].NodeType)
}

func TestComputeCachedStepAssetSingleflightReturnsClones(t *testing.T) {
	start := time.Now().Add(-time.Hour).Truncate(time.Minute)
	end := start.Add(time.Hour)
	window := opencost.NewClosedWindow(start, end)

	cm := &CostModel{assetStepCache: cache.New(5*time.Minute, 10*time.Minute)}
	var calls atomic.Int32
	compute := func() (*assetStepCacheValue, error) {
		calls.Add(1)
		time.Sleep(150 * time.Millisecond)
		node := opencost.NewNode("node-a", "cluster-a", "provider-a", start, end, window)
		node.CPUCost = 3
		return &assetStepCacheValue{
			assetSet: opencost.NewAssetSet(start, end, node),
		}, nil
	}
	clone := func(value *assetStepCacheValue) *assetStepCacheValue {
		return &assetStepCacheValue{assetSet: value.assetSet.Clone()}
	}

	const callers = 12
	startGate := make(chan struct{})
	results := make(chan *opencost.AssetSet, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			value, err := computeCachedStep(
				cm,
				assetStepCacheLayer,
				assetStepCacheKey(start, end, 0),
				cm.assetStepCache,
				5*time.Minute,
				compute,
				clone,
			)
			if err != nil {
				errs <- err
				return
			}
			results <- value.assetSet
		}()
	}

	close(startGate)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), calls.Load(), "concurrent cache misses should share one computation")

	sets := make([]*opencost.AssetSet, 0, callers)
	for set := range results {
		sets = append(sets, set)
	}
	require.Len(t, sets, callers)

	extra := opencost.NewNode("node-b", "cluster-a", "provider-b", start, end, window)
	require.NoError(t, sets[0].Insert(extra, nil))
	assert.Equal(t, 1, sets[1].Length())
	assert.Equal(t, 2, sets[0].Length())
}

func TestComputeCachedStepDoesNotCacheErrors(t *testing.T) {
	cm := &CostModel{allocStepCache: cache.New(5*time.Minute, 10*time.Minute)}
	var calls atomic.Int32
	computeErr := errors.New("compute failed")
	compute := func() (*allocStepCacheValue, error) {
		calls.Add(1)
		return nil, computeErr
	}
	clone := func(value *allocStepCacheValue) *allocStepCacheValue {
		return value
	}

	for range 2 {
		value, err := computeCachedStep(
			cm,
			allocationStepCacheLayer,
			"error-key",
			cm.allocStepCache,
			time.Minute,
			compute,
			clone,
		)
		require.ErrorIs(t, err, computeErr)
		assert.Nil(t, value)
	}

	assert.Equal(t, int32(2), calls.Load())
	assert.Equal(t, 0, cm.allocStepCache.ItemCount())
}

func TestStepCacheEpochKeepsInflightResultOutOfNewGeneration(t *testing.T) {
	cm := &CostModel{allocStepCache: cache.New(5*time.Minute, 10*time.Minute)}
	oldKey := cm.stepCacheKey("step")
	started := make(chan struct{})
	release := make(chan struct{})
	oldDone := make(chan error, 1)

	go func() {
		_, err := computeCachedStep(
			cm,
			allocationStepCacheLayer,
			oldKey,
			cm.allocStepCache,
			time.Minute,
			func() (*allocStepCacheValue, error) {
				close(started)
				<-release
				return &allocStepCacheValue{
					allocSet: opencost.NewAllocationSet(time.Now(), time.Now()),
				}, nil
			},
			func(value *allocStepCacheValue) *allocStepCacheValue {
				return &allocStepCacheValue{allocSet: value.allocSet.Clone()}
			},
		)
		oldDone <- err
	}()

	<-started
	cm.flushStepCaches()
	newKey := cm.stepCacheKey("step")
	require.NotEqual(t, oldKey, newKey)
	close(release)
	require.NoError(t, <-oldDone)

	_, found := cm.allocStepCache.Get(newKey)
	assert.False(t, found, "an old in-flight computation must not populate the new cache generation")
	_, found = cm.allocStepCache.Get(oldKey)
	assert.True(t, found, "the old computation may only write its captured generation")
}

func TestComputeCachedStepDisabledDoesNotCache(t *testing.T) {
	tests := []struct {
		name  string
		cache *cache.Cache
		ttl   time.Duration
	}{
		{name: "nil cache", cache: nil, ttl: time.Minute},
		{name: "zero ttl", cache: cache.New(5*time.Minute, 10*time.Minute), ttl: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &CostModel{allocStepCache: tt.cache}
			var calls atomic.Int32
			compute := func() (*allocStepCacheValue, error) {
				calls.Add(1)
				return &allocStepCacheValue{allocSet: opencost.NewAllocationSet(time.Now(), time.Now())}, nil
			}
			clone := func(value *allocStepCacheValue) *allocStepCacheValue {
				return &allocStepCacheValue{allocSet: value.allocSet.Clone()}
			}

			for range 2 {
				_, err := computeCachedStep(
					cm,
					allocationStepCacheLayer,
					"disabled-key",
					tt.cache,
					tt.ttl,
					compute,
					clone,
				)
				require.NoError(t, err)
			}

			assert.Equal(t, int32(2), calls.Load())
			if tt.cache != nil {
				assert.Equal(t, 0, tt.cache.ItemCount())
			}
		})
	}
}
