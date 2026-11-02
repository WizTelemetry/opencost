package costmodel

import (
	"fmt"
	"time"

	"github.com/patrickmn/go-cache"
)

const (
	allocationStepCacheLayer = "allocation_step"
	assetStepCacheLayer      = "asset_step"
)

// computeCachedStep coalesces concurrent calculations for the same step and
// caches immutable results. Every caller receives a private clone because
// allocation and asset sets are mutable.
func computeCachedStep[T any](
	cm *CostModel,
	layer string,
	key string,
	cache *cache.Cache,
	ttl time.Duration,
	compute func() (T, error),
	clone func(T) T,
) (T, error) {
	var zero T

	cacheKey := key
	groupKey := layer + ":" + key
	cacheEnabled := cache != nil && ttl > 0

	if cacheEnabled {
		if value, found := cache.Get(cacheKey); found {
			if typed, ok := value.(T); ok {
				recordQueryCacheResult(layer, true)
				return clone(typed), nil
			}
			cache.Delete(cacheKey)
		}
		recordQueryCacheResult(layer, false)
	}

	value, err, shared := cm.requestGroup().Do(groupKey, func() (any, error) {
		// Another request may have populated the cache between the lookup above
		// and joining the singleflight group.
		if cacheEnabled {
			if cached, found := cache.Get(cacheKey); found {
				if typed, ok := cached.(T); ok {
					return typed, nil
				}
				cache.Delete(cacheKey)
			}
		}

		start := time.Now()
		computed, computeErr := compute()
		observeQueryComputeDuration(layer, time.Since(start))
		if computeErr != nil {
			recordQueryComputeError(layer)
			return nil, computeErr
		}

		if cacheEnabled {
			cache.Set(cacheKey, computed, ttl)
		}
		return computed, nil
	})

	if shared {
		recordQuerySingleflightShared(layer)
	}
	if err != nil {
		return zero, err
	}

	typed, ok := value.(T)
	if !ok {
		err := fmt.Errorf("unexpected %s cache value type %T", layer, value)
		recordQueryComputeError(layer)
		return zero, err
	}

	return clone(typed), nil
}
