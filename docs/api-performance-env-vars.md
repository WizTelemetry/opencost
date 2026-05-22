# API Performance Environment Variables

This document describes the environment variables used to tune API response caching,
allocation step caching, and cache warming for the canonical prefixed API:

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

OpenCost currently has two relevant cache layers.

| Layer | Scope | Purpose |
|---|---|---|
| Query response cache | HTTP/API response bytes | Makes identical API requests return quickly without re-running the handler. |
| Allocation step cache | Internal allocation computation steps | Reuses expensive allocation computation within model-level queries. |

The query response cache is the most important layer for frontend latency because
it can return a full API response without recomputing allocation, assets, or
efficiency data.

The allocation step cache is still important because it reduces repeated
Prometheus and Go aggregation work when different handlers need compatible
allocation inputs.

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
| `OPENCOST_CACHE_WARMER_ENABLED` | `false` | `true` | Enables the background warmer that periodically calls configured read-only endpoints to keep common query responses hot. |
| `OPENCOST_CACHE_WARMER_INTERVAL_SECONDS` | `300` | `300` | Interval between warmer runs. |
| `OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS` | `30` | `5` to `30` | Delay after startup before the first warm run. Use a longer delay if Prometheus, Kubernetes caches, or pricing data need time to become ready. |
| `OPENCOST_CACHE_WARMER_ENDPOINTS` | Built-in defaults | Frontend-specific list when needed | Semicolon-separated list of read-only endpoints to warm. Leave unset to use the built-in allocation, assets, and efficiency targets. |

## Variable Details

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
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=300
OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS=300
```

This keeps warmed responses alive until the next warmer run.

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

### `OPENCOST_CACHE_WARMER_ENABLED`

Enables or disables the background cache warmer.

When enabled, the warmer runs after startup and then periodically calls
configured read-only API handlers. Successful responses are written to the query
response cache.

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
OPENCOST_CACHE_WARMER_ENABLED=true
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=300
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
OPENCOST_CACHE_WARMER_ENABLED=true
OPENCOST_CACHE_WARMER_INTERVAL_SECONDS=60
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
  - name: OPENCOST_CACHE_WARMER_ENABLED
    value: "true"
  - name: OPENCOST_CACHE_WARMER_INTERVAL_SECONDS
    value: "300"
  - name: OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS
    value: "30"
```

## Operational Notes

- Longer TTLs improve repeated-query latency but can serve slightly stale data.
- `window=7d` and other relative windows are realtime windows because their end
  time tracks `now`.
- The warmer only helps if it warms the same endpoint and query string the
  frontend later requests.
- The warmer interval should not exceed the realtime query cache TTL unless some
  cold-cache requests are acceptable.
- Setting `OPENCOST_QUERY_CACHE_TTL_SECONDS=0` disables the query response cache,
  which also makes response cache warming ineffective.
- Query response cache keys include the canonical path and normalized query
  string. Avoid sending empty frontend parameters such as `filter=` or `step=`
  unless they are semantically required.
- For prefixed API integration, use the canonical route set in external clients,
  but cache behavior is shared with compatible legacy aliases.

