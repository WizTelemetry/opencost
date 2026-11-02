# API Performance Environment Variables

This document describes the environment variables used to tune API response caching,
query step caching, and cache warming for the canonical prefixed API:

```text
/kapis/costwise.wiztelemetry.io/v1alpha1
```

These settings are intended to reduce user-visible latency for repeated frontend
queries such as:

```text
/allocation?window=7d&aggregate=namespace&accumulate=true&includeIdle=true
/assets?window=7d&aggregate=type&accumulate=true
/efficiency?window=7d&aggregate=namespace
```

## Cache Layers

OpenCost has four relevant cache layers.

| Layer | Scope | Purpose |
|---|---|---|
| Query response cache | HTTP/API response bytes | Makes identical API requests return quickly without re-running the handler. |
| Allocation step cache | Internal allocation computation steps | Reuses expensive allocation computation within model-level queries. |
| Asset step cache | Internal asset computation steps | Reuses node, disk, load balancer, and cluster-management asset calculations required for idle allocation. |
| Lightweight ETL store | Immutable hourly and daily allocation buckets on disk | Lets unrelated queries reuse precomputed time ranges across process restarts and across different aggregation/filter parameters. |

The query response cache is the most important layer for frontend latency because
it can return a full API response without recomputing allocation, assets, or
efficiency data.

The step caches are still important because they reduce repeated Prometheus and
Go aggregation work when different handlers need compatible allocation or asset
inputs. Concurrent misses for the same step are coalesced with singleflight, and
each caller receives a deep copy so aggregation cannot mutate the cached value.

## Realtime vs Historical Windows

Cache TTL selection depends on the query window end time.

| Window type | Rule | Example |
|---|---|---|
| Realtime | Window end is within the last 1 hour | `window=7d`, `window=24h`, `window=1h` |
| Historical | Window ended more than 1 hour ago | `window=2026-05-01T00:00:00Z,2026-05-08T00:00:00Z` |

Relative windows such as `window=7d` usually end at `now`, so they are treated as
realtime even though they include historical data.

For example, at `2026-05-22 15:00:00`, this request:

```text
/allocation?window=7d
```

roughly means:

```text
2026-05-15 15:00:00 -> 2026-05-22 15:00:00
```

At `2026-05-22 15:05:00`, the same request means:

```text
2026-05-15 15:05:00 -> 2026-05-22 15:05:00
```

Because the end of the window tracks the current time, `window=7d` uses the
realtime TTL settings.

## Environment Variables

| Variable | Default | Recommended | Description |
|---|---:|---:|---|
| `OPENCOST_QUERY_CACHE_TTL_SECONDS` | `60` | `300` | Global query cache baseline. Also controls whether the query cache is created. Set to `0` to disable the query response cache. |
| `OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS` | `30` | `300` or `360` | Query response cache TTL for realtime windows such as `window=7d`. This should be at least as long as the cache warmer interval. |
| `OPENCOST_HISTORICAL_QUERY_CACHE_TTL_SECONDS` | `300` | `600` | Query response cache TTL for windows ending more than 1 hour ago. Historical data changes less often, so it can usually be cached longer. |
| `OPENCOST_ALLOCATION_STEP_CACHE_TTL_SECONDS` | `30` | `300` | Internal allocation step cache TTL for realtime allocation windows. |
| `OPENCOST_ALLOCATION_HISTORICAL_STEP_CACHE_TTL_SECONDS` | `600` | `600` to `1800` | Internal allocation step cache TTL for historical allocation windows. |
| `OPENCOST_ASSET_STEP_CACHE_TTL_SECONDS` | `30` | `300` | Internal asset step cache TTL for realtime asset windows. |
| `OPENCOST_ASSET_HISTORICAL_STEP_CACHE_TTL_SECONDS` | `600` | `600` to `1800` | Internal asset step cache TTL for historical asset windows. |
| `OPENCOST_ETL_ENABLED` | `false` | `true` | Enables background allocation bucket computation and API reads from the ETL store. |
| `OPENCOST_ETL_STORAGE_DIR` | `/var/configs/costwise-etl` | A persistent-volume path | Directory used for immutable ETL bucket files. |
| `OPENCOST_ETL_RESOLUTION_SECONDS` | `3600` | `3600` | Base ETL bucket resolution. Smaller values improve boundary precision but increase Prometheus and disk load. |
| `OPENCOST_ETL_HOURLY_STORE_DURATION_HOURS` | `168` | `168` | Number of recent hours covered by hourly buckets. |
| `OPENCOST_ETL_DAILY_STORE_DURATION_DAYS` | `30` | `30` to `91` | Number of completed days covered by daily buckets. |
| `OPENCOST_ETL_INTERVAL_SECONDS` | `300` | `300` | Background ETL worker interval. |
| `OPENCOST_ETL_STARTUP_DELAY_SECONDS` | `30` | `10` to `60` | Delay before the first ETL run after startup. |
| `OPENCOST_ETL_QUERY_CONCURRENCY` | `2` | `2` to `5` | Concurrent ETL bucket builds. Higher values increase Prometheus pressure. |
| `OPENCOST_ETL_MAX_HOURLY_BUCKETS_PER_RUN` | `48` | Tune to Prometheus capacity | Maximum missing/refreshed hourly buckets built in one worker run. |
| `OPENCOST_ETL_MAX_DAILY_BUCKETS_PER_RUN` | `7` | Tune to Prometheus capacity | Maximum missing/refreshed daily buckets built in one worker run. |
| `OPENCOST_ETL_REFRESH_HOURLY_BUCKETS` | `2` | `2` | Maximum stale hourly buckets refreshed per run within the late-data lookback. |
| `OPENCOST_ETL_REFRESH_DAILY_BUCKETS` | `1` | `1` | Maximum stale daily buckets refreshed per run within the late-data lookback. |
| `OPENCOST_ETL_LATE_DATA_HOURLY_LOOKBACK_HOURS` | `48` | `48` | Recent hourly range eligible for rolling rebuilding when Prometheus backfills data. |
| `OPENCOST_ETL_LATE_DATA_DAILY_LOOKBACK_DAYS` | `3` | `3` | Recent daily range eligible for rolling rebuilding. |
| `OPENCOST_ETL_LATE_DATA_REFRESH_INTERVAL_SECONDS` | `1800` | `1800` | Minimum age before a bucket in the late-data lookback is rebuilt again. |
| `OPENCOST_ETL_MINIMUM_COVERAGE_PERCENT` | `50` | `50` | Minimum bucket coverage required before an API query combines ETL and live-computed edge ranges. |
| `OPENCOST_CACHE_WARMER_ENABLED` | `false` | `true` | Enables the background warmer that periodically calls configured read-only endpoints to keep common query responses hot. |
| `OPENCOST_CACHE_WARMER_INTERVAL_SECONDS` | `300` | `240` with a `300` TTL | Interval between warmer runs. |
| `OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS` | `30` | `5` to `30` | Delay after startup before the first warm run. Use a longer delay if Prometheus, Kubernetes caches, or pricing data need time to become ready. |
| `OPENCOST_CACHE_WARMER_ENDPOINTS` | Built-in defaults | Frontend-specific list when needed | Semicolon-separated list of read-only endpoints to warm. Leave unset to use the built-in allocation, assets, and efficiency targets. |

## Variable Details

### Lightweight ETL

When ETL is enabled, allocation data is computed in the background and stored as
revisioned Bingen data files with separate readiness markers:

```text
Prometheus -> allocation ETL worker -> hourly/daily buckets + .ready markers -> API composition
```

The API composes aligned daily buckets first, then hourly buckets, and computes
only the uncovered leading/trailing ranges live. Filters and aggregation are
still applied to the raw composed allocation data, so query parameters do not
need to be precomputed individually.

Assets are intentionally not persisted or composed from ETL buckets. Node and
other asset costs use window-dependent PromQL such as `avg_over_time`, so hourly
or daily `AssetSet` values are not additive across arbitrary windows. The asset
step cache still avoids repeated computation for identical windows, and the
live asset result is used to calculate `includeIdle=true` exactly.

Relative windows such as `window=7d` can round their requested end forward to
the next day boundary. Allocation and asset computations clamp that future tail
to the latest data-resolution boundary. This prevents Prometheus range queries
from extrapolating incomplete calendar buckets while keeping cache keys stable
between requests within the same resolution interval.

The ETL worker is incremental. `OPENCOST_ETL_MAX_HOURLY_BUCKETS_PER_RUN` and
`OPENCOST_ETL_MAX_DAILY_BUCKETS_PER_RUN` limit first-run backfill and Prometheus
pressure. Store the ETL directory on persistent storage; otherwise the backlog
must be rebuilt after every Pod restart.

The worker only commits a bucket after the data-source coverage window includes
the complete bucket. That prevents startup races, empty Prometheus responses, or
a partially available history range from being persisted as a finished bucket.
An empty allocation result is still valid when source coverage is complete, which
supports clusters that legitimately have no workloads in a time range.

Recent buckets remain eligible for rolling rebuilds so late Prometheus data can
replace provisional results. Missing buckets always have priority over refresh
work, and the refresh interval prevents the worker from repeatedly rebuilding
the same bucket on every five-minute run.

Cost-affecting pricing configuration is included in the ETL path as a
non-reversible fingerprint. Changing custom prices or discounts therefore
switches reads and writes to a new revision instead of mixing old-priced and
new-priced history. Response, allocation-step, and asset-step caches are flushed
when the pricing monitor detects a change.

Retention values define both the queryable history and the cleanup boundary.
Expired hourly/daily files, readiness markers, and superseded pricing revisions
are removed by the ETL worker. ETL schema v2 ignores legacy v1 buckets that were
written before attribution metadata preservation was fixed.

Inspect readiness and recent build results at:

```text
/kapis/costwise.wiztelemetry.io/v1alpha1/etl/status
```

Recommended starting configuration:

```text
OPENCOST_ETL_ENABLED=true
OPENCOST_ETL_STORAGE_DIR=/mnt/export/etl
OPENCOST_ETL_RESOLUTION_SECONDS=3600
OPENCOST_ETL_HOURLY_STORE_DURATION_HOURS=168
OPENCOST_ETL_DAILY_STORE_DURATION_DAYS=30
OPENCOST_ETL_INTERVAL_SECONDS=300
OPENCOST_ETL_QUERY_CONCURRENCY=2
OPENCOST_ETL_MAX_HOURLY_BUCKETS_PER_RUN=48
OPENCOST_ETL_MAX_DAILY_BUCKETS_PER_RUN=7
```

### `OPENCOST_QUERY_CACHE_TTL_SECONDS`

Controls the global baseline TTL for the query response cache.

It has two important effects:

- If the value is `0` or negative, the query response cache is disabled.
- If the cache is enabled, this value is used as a fallback baseline for some TTL decisions.

It does not always directly control the final TTL for a request. Realtime and
historical override variables take precedence when they are set.

For `window=7d`, configure `OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS` explicitly
if you want the response cache to live longer than the default `30s`.

### `OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS`

Controls query response cache TTL for windows ending within the last hour.

This is the key setting for common frontend queries using relative windows:

```text
window=7d
window=24h
window=1h
```

If cache warming is enabled, this value should be greater than or equal to
`OPENCOST_CACHE_WARMER_INTERVAL_SECONDS`.

Example:

```text
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=240
OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=300
```

The 60-second margin avoids a cold window if one warm run is delayed.

For more tolerance, use:

```text
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=300
OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=360
```

### `OPENCOST_HISTORICAL_QUERY_CACHE_TTL_SECONDS`

Controls query response cache TTL for windows ending more than 1 hour ago.

Historical queries are usually safer to cache longer because the data is less
likely to change. A value between `600` and `1800` seconds is usually reasonable
for dashboard-heavy environments.

Use a shorter value if your Prometheus data backfills frequently or your pricing
configuration changes often.

### `OPENCOST_ALLOCATION_STEP_CACHE_TTL_SECONDS`

Controls internal allocation step cache TTL for realtime allocation windows.

This cache does not directly store the full HTTP response. It stores expensive
intermediate allocation results so related API queries can reuse them.

Use this alongside `OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS`; setting only one
of them can leave part of the request path cold.

### `OPENCOST_ALLOCATION_HISTORICAL_STEP_CACHE_TTL_SECONDS`

Controls internal allocation step cache TTL for historical allocation windows.

Historical allocation inputs are usually stable, so this value can be longer than
the realtime allocation step cache TTL.

### `OPENCOST_ASSET_STEP_CACHE_TTL_SECONDS`

Controls internal asset step cache TTL for realtime asset windows.

Asset steps are required when `includeIdle=true`. Without this cache, every
allocation request in the same period can independently recompute node, disk,
load balancer, and cluster-management assets.

### `OPENCOST_ASSET_HISTORICAL_STEP_CACHE_TTL_SECONDS`

Controls internal asset step cache TTL for historical asset windows. Historical
asset inputs are usually stable and can use a longer TTL than realtime assets.

### `OPENCOST_CACHE_WARMER_ENABLED`

Enables or disables the background cache warmer.

When enabled, the warmer runs after startup and then periodically calls
configured read-only API handlers. It bypasses the query response cache on read
so each run refreshes both the response and the underlying step caches.

The warmer is most useful when:

- frontend users commonly load the same dashboard queries;
- the first uncached request is expensive;
- the query response cache TTL is long enough to last until the next warm run.

### `OPENCOST_CACHE_WARMER_INTERVAL_SECONDS`

Controls how often the warmer runs.

This should be coordinated with realtime query cache TTL:

```text
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS <= OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS
```

If the interval is longer than the TTL, the cache can expire before the next
warmer run.

Bad example:

```text
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=300
OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=30
```

In this setup, the warmed response expires after 30 seconds, leaving a 270-second
gap before the next warmer run.

### `OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS`

Controls how long the warmer waits after process startup before the first warm.

Use a small value such as `5` seconds when the process and Prometheus are ready
quickly. Use `30` seconds or longer when startup commonly involves cache syncs,
pricing initialization, or slow Prometheus availability.

If this value is too short, the first warm run may fail or compute against
incomplete startup state.

### `OPENCOST_CACHE_WARMER_ENDPOINTS`

Overrides the built-in warmer endpoint list.

Format:

```text
/path?query=value;/path2?query=value
```

Only read-only GET-style endpoints should be configured. Mutating endpoints must
not be warmed.

The path may be either legacy or prefixed. Cache keys are normalized so these are
equivalent for the same canonical endpoint:

```text
/allocation?window=7d&aggregate=namespace
/kapis/costwise.wiztelemetry.io/v1alpha1/allocation?window=7d&aggregate=namespace
```

Keep endpoint query strings aligned with actual frontend requests. Different
query parameters produce different cache keys.

For example, these are different cache entries:

```text
/allocation?window=7d&aggregate=namespace&accumulate=true
/allocation?window=7d&aggregate=namespace&accumulate=true&includeIdle=true
```

## Recommended Configurations

### General Frontend Dashboard

Use this when users mostly load common allocation, assets, and efficiency views.

```text
OPENCOST_QUERY_CACHE_TTL_SECONDS=300
OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=300
OPENCOST_HISTORICAL_QUERY_CACHE_TTL_SECONDS=600
OPENCOST_ALLOCATION_STEP_CACHE_TTL_SECONDS=300
OPENCOST_ALLOCATION_HISTORICAL_STEP_CACHE_TTL_SECONDS=600
OPENCOST_ASSET_STEP_CACHE_TTL_SECONDS=300
OPENCOST_ASSET_HISTORICAL_STEP_CACHE_TTL_SECONDS=600
OPENCOST_CACHE_WARMER_ENABLED=true
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=240
OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS=30
```

### More Stable Dashboard Cache

Use this when slightly stale dashboard data is acceptable and avoiding cold
requests is more important.

```text
OPENCOST_QUERY_CACHE_TTL_SECONDS=360
OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=360
OPENCOST_HISTORICAL_QUERY_CACHE_TTL_SECONDS=1200
OPENCOST_ALLOCATION_STEP_CACHE_TTL_SECONDS=360
OPENCOST_ALLOCATION_HISTORICAL_STEP_CACHE_TTL_SECONDS=1200
OPENCOST_ASSET_STEP_CACHE_TTL_SECONDS=360
OPENCOST_ASSET_HISTORICAL_STEP_CACHE_TTL_SECONDS=1200
OPENCOST_CACHE_WARMER_ENABLED=true
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=300
OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS=30
```

### Conservative Configuration

Use this when freshness is more important than minimizing cold-cache latency.

```text
OPENCOST_QUERY_CACHE_TTL_SECONDS=60
OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=60
OPENCOST_HISTORICAL_QUERY_CACHE_TTL_SECONDS=300
OPENCOST_ALLOCATION_STEP_CACHE_TTL_SECONDS=60
OPENCOST_ALLOCATION_HISTORICAL_STEP_CACHE_TTL_SECONDS=600
OPENCOST_ASSET_STEP_CACHE_TTL_SECONDS=60
OPENCOST_ASSET_HISTORICAL_STEP_CACHE_TTL_SECONDS=600
OPENCOST_CACHE_WARMER_ENABLED=true
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=50
OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS=30
```

This configuration uses a shorter warmer interval to avoid TTL gaps.

## Custom Warmer Endpoint Example

If the frontend mostly uses namespace allocation and assets graph queries, set:

```text
OPENCOST_CACHE_WARMER_ENDPOINTS=/allocation?window=7d&aggregate=namespace&accumulate=true&includeIdle=true;/allocation/summary?window=7d&aggregate=namespace&accumulate=true;/allocation/summary/topline?window=7d&aggregate=namespace&accumulate=true;/assets/graph?window=7d&aggregate=type
```

Add endpoints only when they match real traffic. Warming many endpoints can move
load from users to the background worker, but it does not remove the underlying
compute cost.

### CostWise Overview

The KubeSphere CostWise overview issues seven distinct allocation queries. One
warm run for the common cluster summary computes the shared daily allocation and
asset steps; the other current-period variants reuse those steps.

```text
OPENCOST_CACHE_WARMER_ENDPOINTS=/allocation?window=7d&aggregate=cluster&includeIdle=true;/allocation?window=7d%20offset%207d&aggregate=cluster&includeIdle=true;/allocation?window=7d&step=1d&aggregate=cluster&includeIdle=true;/allocation?window=7d&aggregate=label:kubesphere_io_workspace&includeIdle=true;/allocation?window=7d&step=1d&aggregate=label:kubesphere_io_workspace&includeIdle=true;/allocation?window=7d&aggregate=cluster,namespace&includeIdle=true;/allocation?window=7d&step=1d&aggregate=cluster,namespace&includeIdle=true
```

## Kubernetes Deployment Example

Example container environment:

```yaml
env:
  - name: OPENCOST_QUERY_CACHE_TTL_SECONDS
    value: "300"
  - name: OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS
    value: "300"
  - name: OPENCOST_HISTORICAL_QUERY_CACHE_TTL_SECONDS
    value: "600"
  - name: OPENCOST_ALLOCATION_STEP_CACHE_TTL_SECONDS
    value: "300"
  - name: OPENCOST_ALLOCATION_HISTORICAL_STEP_CACHE_TTL_SECONDS
    value: "600"
  - name: OPENCOST_ASSET_STEP_CACHE_TTL_SECONDS
    value: "300"
  - name: OPENCOST_ASSET_HISTORICAL_STEP_CACHE_TTL_SECONDS
    value: "600"
  - name: OPENCOST_CACHE_WARMER_ENABLED
    value: "true"
  - name: OPENCOST_CACHE_WARMER_INTERVAL_SECONDS
    value: "240"
  - name: OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS
    value: "30"
```

## Operational Notes

- Longer TTLs improve repeated-query latency but can serve slightly stale data.
- `window=7d` and other relative windows are realtime windows because their end
  time tracks `now`.
- Response cache hits require the same endpoint and normalized query string.
  Compatible windows can still reuse warmed allocation and asset steps even when
  the HTTP query differs.
- The warmer interval should not exceed the realtime query cache TTL unless some
  cold-cache requests are acceptable.
- Setting `OPENCOST_QUERY_CACHE_TTL_SECONDS=0` disables the query response cache,
  which also makes response cache warming ineffective.
- Step caches remain active when the query response cache is disabled unless
  their own TTL variables are set to `0`.
- Monitor `opencost_query_cache_hits_total`, `opencost_query_cache_misses_total`,
  `opencost_query_singleflight_shared_total`,
  `opencost_query_compute_duration_seconds`, and
  `opencost_query_errors_total` by `layer`.
- Query response cache keys include the canonical path and normalized query
  string. Avoid sending empty frontend parameters such as `filter=` or `step=`
  unless they are semantically required.
- For prefixed API integration, use the canonical route set in external clients,
  but cache behavior is shared with compatible legacy aliases.

