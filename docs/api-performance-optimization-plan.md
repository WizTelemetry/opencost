# Prefixed API Performance Optimization Plan

## Background

The frontend integrates with the canonical prefixed API path:

```text
/kapis/costwise.wiztelemetry.io/v1alpha1
```

The main performance concern is around the allocation, assets, and efficiency API families. Current measurements show that some endpoints are slow on first access and faster on repeated access, which indicates that in-memory caching is working but cold-cache behavior is still expensive.

Kubecost deployments can respond faster because they commonly use ETL-style precomputation and local hourly/daily stores. Example Kubecost-related settings:

| Setting | Example | Meaning |
|---|---:|---|
| `ETL_RESOLUTION_SECONDS` | `300` | ETL runs at a 5-minute resolution. |
| `ETL_MAX_PROMETHEUS_QUERY_DURATION_MINUTES` | `1440` | Prometheus queries are bounded to 1-day chunks. |
| `ETL_DAILY_STORE_DURATION_DAYS` | `91` | Daily ETL data is retained for 91 days. |
| `ETL_HOURLY_STORE_DURATION_HOURS` | `49` | Hourly ETL data is retained for about 2 days. |
| `MAX_QUERY_CONCURRENCY` | `5` | Prometheus query concurrency is controlled. |

The current OpenCost path is closer to:

```text
API request -> Prometheus queries -> Go aggregation -> short TTL cache
```

Kubecost is closer to:

```text
Prometheus -> ETL precomputation -> hourly/daily store -> API reads precomputed data
```

The optimization plan below prioritizes low-risk improvements first, then moves toward a lightweight ETL architecture.

For deployment-level configuration details, see [API Performance Environment Variables](api-performance-env-vars.md).

## Current Observations

Focused benchmark target:

```text
allocation, assets, efficiency prefixed GET endpoints
```

Recent measurements with `warmups=1` and `runs=3`:

| API | Status | Warmup ms | Avg ms | Notes |
|---|---:|---:|---:|---|
| `/allocation` | `200` | `7821.40` | `214.26` | Cold cache is expensive; warm cache is fast. |
| `/allocation/compute` | `200` | `1571.32` | `212.67` | Similar allocation compute path. |
| `/assets` | `200` | `1733.40` | `158.82` | Cold cache cost is visible. |
| `/assets/graph` | `200` | `1751.12` | `1684.57` | Still slow after warmup; needs deeper optimization. |
| `/efficiency/clusters` | `200` | `1782.28` | `1731.05` | Still slow after warmup. |
| `/efficiency/clusters/summary` | `200` | `2122.33` | `1984.09` | Still slow after warmup. |
| `/assets/carbon` | `404` | `179.48` | `150.09` | Confirm whether this prefixed route should exist. |

The first request being slow and later requests being fast means cache is working, but the cache is too short-lived or not broad enough for the expected user experience.

## Stage 1: Low-Risk Quick Wins

Goal: improve user-perceived latency without changing core cost calculation semantics.

### 1. Make Query Cache TTL More Configurable

Current behavior:

- Global default query cache TTL is `60s`.
- Near-realtime windows are capped around `30s`.
- Allocation step cache also uses around `30s` for recent windows.

Recommended changes:

- Keep conservative defaults.
- Add explicit environment variables for different cache classes.
- Allow deployments to set `300s` or `600s` for frontend-heavy environments.

Suggested environment variables:

```text
OPENCOST_QUERY_CACHE_TTL_SECONDS=300
OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=300
OPENCOST_HISTORICAL_QUERY_CACHE_TTL_SECONDS=600
OPENCOST_ALLOCATION_STEP_CACHE_TTL_SECONDS=300
OPENCOST_ALLOCATION_HISTORICAL_STEP_CACHE_TTL_SECONDS=600
```

Implementation notes:

- Update `cacheTTLForWindow`.
- Update `allocStepCacheTTL`.
- Preserve backward-compatible behavior when new env vars are unset.
- Add unit tests for default, realtime override, historical override, and disabled cache behavior.

### 2. Add a Cache Warmer

Add a background cache warmer that runs after startup and then periodically refreshes common API queries.

Initial warm targets:

```text
/allocation?window=7d&aggregate=namespace&accumulate=true&includeIdle=true
/allocation/summary?window=7d&aggregate=namespace&accumulate=true
/allocation/summary/topline?window=7d&aggregate=namespace&accumulate=true
/assets?window=7d&aggregate=type&accumulate=true
/assets/graph?window=7d&aggregate=type
/efficiency?window=7d&aggregate=namespace
/efficiency/clusters?window=7d&step=1d&accumulate=true
/efficiency/clusters/summary?window=7d&step=1d&accumulate=true
```

Suggested environment variables:

```text
OPENCOST_CACHE_WARMER_ENABLED=true
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=300
OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS=30
OPENCOST_CACHE_WARMER_ENDPOINTS=/allocation?...;/assets?...
```

Implementation notes:

- Prefer calling handler/model functions internally instead of HTTP-looping when practical.
- If internal calls are too invasive, an HTTP-based warmer is acceptable as a first iteration.
- Log each warm result with endpoint, status, duration, and error.
- Do not warm mutating endpoints.

### 3. Standardize Frontend Query Parameters

Cache keys include the request path and encoded query string. Different parameter presence means different cache keys.

Avoid frontend variation such as:

```text
filter=
cluster=
step=
accumulate=
```

when the parameter is semantically empty.

Recommended frontend rules:

- Do not send empty query parameters.
- Use stable defaults for `window`, `aggregate`, `step`, `accumulate`, `includeIdle`, and `filter`.
- For `/assets/graph`, do not combine `step` with `accumulate`.
- For `/assets/graph`, use:

```text
window=7d&aggregate=type
```

unless the UI truly needs pagination or a different aggregate.

### 4. Add Cache and Duration Observability

Add logs or metrics for:

- endpoint
- HTTP path
- cache hit or miss
- cache key
- response status
- handler total duration
- model compute duration
- Prometheus query duration
- response size

Recommended Prometheus metrics:

```text
opencost_api_request_duration_seconds{endpoint,status,cache="hit|miss"}
opencost_api_cache_hits_total{endpoint}
opencost_api_cache_misses_total{endpoint}
opencost_prometheus_query_duration_seconds{query_name}
opencost_cache_warmer_duration_seconds{endpoint,status}
```

## Stage 2: Target Slow Endpoints

Goal: reduce cold compute cost and improve endpoints that remain slow even after warmup.

### 1. Add Response Cache to `/assets/graph`

`/assets` uses query response caching, but `/assets/graph` should be checked and aligned with the same caching behavior.

Expected behavior:

- First request computes.
- Subsequent identical requests return from query cache.
- TTL follows `cacheTTLForWindow`.

Tests:

- cache hit test for identical `/assets/graph` request
- cache key changes when query parameters change
- no cache write on error response

### 2. Confirm `/assets/carbon` Contract

Current focused benchmark returned `404`.

Decision required:

- If frontend needs `/assets/carbon`, register and test the prefixed route.
- If frontend does not need it, remove it from Swagger or mark it unsupported.

Follow repository API rules:

- Runtime compatibility may include legacy aliases.
- Swagger must expose only prefixed canonical paths.

### 3. Reuse Allocation Results for Efficiency

Efficiency endpoints are derived from allocation-like data. Avoid recalculating expensive allocation inputs when a compatible allocation result is already cached.

Targets:

```text
/efficiency
/efficiency/clusters
/efficiency/clusters/summary
```

Implementation direction:

- Check whether efficiency paths call allocation query paths with identical windows and compatible aggregation.
- Reuse allocation step cache or query cache where possible.
- Avoid recomputing allocation for each efficiency variant in the same time window.

### 4. Normalize Relative Window Cache Keys

If relative windows resolve to precise current timestamps, cache keys can change frequently.

For example:

```text
window=7d
```

can produce slightly different start/end times depending on request time.

Recommended approach:

- Normalize relative windows to a fixed bucket.
- Use 1-minute or 5-minute rounding for near-realtime windows.
- Keep exact timestamps exact when users provide explicit absolute windows.

This can significantly improve cache hit rate for frontend polling and refreshes.

## Stage 3: Lightweight ETL / Pre-Aggregation

Goal: move from request-time Prometheus computation toward Kubecost-like precomputed reads.

### 1. Add an ETL Worker

Initial worker behavior:

- Runs every `5m`.
- Computes common allocation, asset, and efficiency aggregates.
- Stores hourly and daily buckets.

Suggested environment variables:

```text
OPENCOST_ETL_ENABLED=true
OPENCOST_ETL_RESOLUTION_SECONDS=300
OPENCOST_ETL_HOURLY_STORE_DURATION_HOURS=49
OPENCOST_ETL_DAILY_STORE_DURATION_DAYS=91
OPENCOST_ETL_MAX_PROMETHEUS_QUERY_DURATION_MINUTES=1440
OPENCOST_ETL_QUERY_CONCURRENCY=5
```

### 2. Start With Narrow Pre-Aggregates

Do not try to implement full Kubecost ETL immediately. Start with the frontend’s most common views:

Allocation:

```text
window=7d, aggregate=namespace, accumulate=true
window=7d, aggregate=cluster, accumulate=true
```

Assets:

```text
window=7d, aggregate=type
window=7d, aggregate=name
```

Efficiency:

```text
window=7d, aggregate=namespace
window=7d, cluster summary
```

### 3. Store Precomputed Results

Start with one of:

- local disk
- PVC
- object storage
- embedded key-value store

Store keys should include:

- data type
- window bucket
- aggregate
- cluster or namespace filters when applicable
- schema version

Example:

```text
allocation/daily/2026-05-21/aggregate=namespace/schema=v1
assets/daily/2026-05-21/aggregate=type/schema=v1
efficiency/daily/2026-05-21/aggregate=namespace/schema=v1
```

### 4. Prefer Precomputed Reads in API Handlers

API lookup order:

```text
query response cache -> ETL store -> live compute -> cache/store write
```

Rules:

- If the query is fully covered by ETL buckets, return ETL data.
- If only recent partial data is missing, combine ETL data with live computation for the missing range.
- If filters are unsupported by ETL, fall back to live compute.

### 5. Add ETL Status Endpoint

Expose enough data to debug slow requests:

```text
/kapis/costwise.wiztelemetry.io/v1alpha1/etl/status
```

Suggested response fields:

- ETL enabled
- last successful run
- last failed run
- last error
- hourly coverage
- daily coverage
- stored bucket counts
- average Prometheus query duration
- current worker state

## Recommended Agent Task Breakdown

### Task A: Configurable Cache TTL

Scope:

- Add realtime and historical query cache TTL env vars.
- Add allocation step cache TTL env vars.
- Update tests.

Acceptance criteria:

- Defaults preserve current behavior.
- Setting `OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=300` makes recent-window query cache last 5 minutes.
- Setting allocation step cache env vars changes `allocStepCacheTTL`.
- Unit tests cover default, override, and disabled behavior.

### Task B: Cache `/assets/graph` and Verify `/assets/carbon`

Scope:

- Add query response cache to `/assets/graph`.
- Confirm whether `/assets/carbon` should be exposed.
- Add route/cache tests.

Acceptance criteria:

- Repeated `/assets/graph` requests hit response cache.
- Error responses are not cached.
- `/assets/carbon` route behavior matches Swagger.

### Task C: API Performance Metrics

Scope:

- Add request duration, cache hit/miss, and warmer metrics.
- Add Prometheus query duration where query names are available.

Acceptance criteria:

- Metrics identify which endpoint is slow.
- Metrics distinguish cache hit from miss.
- Metrics can show Prometheus query bottlenecks.

### Task D: Cache Warmer

Scope:

- Add background warmer for configured GET endpoints.
- Add env-driven endpoint list and interval.
- Add startup delay.

Acceptance criteria:

- Warmer can be enabled/disabled.
- Warmer logs status and duration.
- Warmer does not run mutating endpoints.
- Frontend common queries are warm after startup.

### Task E: Relative Window Cache Key Normalization

Scope:

- Normalize relative windows to 1-minute or 5-minute buckets.
- Preserve exact absolute window behavior.
- Add tests for cache key stability.

Acceptance criteria:

- Repeated `window=7d` requests within the same bucket share cache keys.
- Absolute timestamp windows are not unexpectedly rounded.

### Task F: Lightweight ETL Prototype

Scope:

- Add ETL worker.
- Store daily/hourly allocation, asset, and efficiency summaries.
- Prefer ETL reads for supported queries.

Acceptance criteria:

- ETL worker writes buckets.
- API can serve supported queries from ETL store.
- Missing buckets fall back to live compute.
- ETL status endpoint reports coverage and errors.

## Recommended Execution Order

1. Task A: Configurable Cache TTL
2. Task B: `/assets/graph` cache and `/assets/carbon` contract
3. Task C: Metrics and logs
4. Task D: Cache warmer
5. Task E: Relative window normalization
6. Task F: Lightweight ETL

Tasks A-D should provide the fastest user-visible improvement with limited architectural risk. Tasks E-F can follow once measurements show the remaining bottlenecks.
