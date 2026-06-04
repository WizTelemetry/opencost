# Environment Variables

This document lists the runtime environment variables currently supported by
this repository. It is based on the environment variable readers and constants in
the current codebase.

This document does not cover test-only variables, build-time arguments such as
`CGO_ENABLED`, or environment variables consumed only by external tooling.

## Configuration Rules

- Boolean values are parsed as Go booleans. Use `true` or `false`.
- Duration values usually support Go duration strings such as `30s`, `5m`, or
  `2h`, unless the variable name explicitly says `SECONDS`, `MINUTES`, `HOURS`,
  or `DAYS`.
- Integer variables should be plain base-10 numbers.
- Secret values should be provided through Kubernetes Secrets or another secret
  manager, not plain ConfigMaps.
- Prefer setting only the variables needed by your deployment. Many variables
  are provider-specific or compatibility switches.

## Recommended Baseline

For a normal Kubernetes deployment using Prometheus and the canonical prefixed
API, start with:

```text
PROMETHEUS_SERVER_ENDPOINT=http://prometheus-server.monitoring.svc:9090
CLUSTER_ID=<stable-cluster-id>
INSTALL_NAMESPACE=opencost
API_PORT=9003
```

For frontend-heavy deployments where first API requests are slow, add the cache
and warmer settings from the "API Performance" section.

## Core Runtime

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `API_PORT` | `9003` | HTTP API port for the cost-model service. | You need a non-default container port. | Prefer leaving at `9003` unless your deployment already reserves that port. |
| `CLUSTER_ID` | empty | Stable cluster identifier used in multi-cluster labeling and filters. | Running multiple clusters or using cluster-scoped Prometheus labels. | Recommended. Use a stable value that does not change across pod restarts. |
| `APP_NAME` | `Opencost` | Application name used by core helpers. | You need a custom app display/name value. | Usually leave unset. |
| `CONFIG_PATH` | `/var/configs` | Base directory for config files and generated local config artifacts. | Mounting config files somewhere else. | Must be writable for features that persist generated config files. |
| `INSTALL_NAMESPACE` | `opencost` | Kubernetes namespace where OpenCost is installed. | Installed outside the default namespace. | Set to the actual namespace in Helm or manifests. |
| `PPROF_ENABLED` | `false` | Enables pprof diagnostic endpoints. | Debugging CPU, memory, or goroutine issues. | Do not enable publicly. Restrict network access. |
| `UTC_OFFSET` | empty / UTC | Offset used when parsing API windows. | You need day/week/month boundaries to align to a local timezone. | Use values like `+08:00` or `-05:00`. Invalid values fall back to UTC. |
| `HELM_VALUES` | empty | Base64-encoded Helm values returned by the `/helmValues` endpoint. | You intentionally expose install values for diagnostics. | May contain sensitive deployment details; leave empty unless required. |

## Logging

These are backed by the Cobra/Viper command setup.

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `LOG_LEVEL` | `info` | Controls log level. | Debugging or reducing log volume. | Common values: `debug`, `info`, `warn`, `error`. |
| `LOG_FORMAT` | `pretty` | Controls log format. | Shipping logs to structured collectors. | Use `json` for production log pipelines. |
| `DISABLE_LOG_COLOR` | `false` | Disables colored pretty logs. | Logs are viewed in systems that do not handle ANSI color. | Mainly relevant with `LOG_FORMAT=pretty`. |

## Kubernetes And Metrics Export

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `KUBERNETES_PORT` | empty | Presence of this variable enables Kubernetes mode. | Running inside Kubernetes. | Usually injected automatically by Kubernetes service discovery. |
| `KUBERNETES_RESOURCE_ACCESS` | `true` | Allows OpenCost to access Kubernetes resources. | Running without Kubernetes resource permissions. | Set `false` only for restricted/non-standard deployments. |
| `KUBECOST_METRICS_POD_ENABLED` | `false` | Indicates a metrics pod is deployed. | Running the agent/metrics pod path. | Usually Helm-managed. |
| `KUBECOST_METRICS_PORT` | `9005` | HTTP port for agent/metrics mode. | Changing the metrics pod port. | Agent command uses this as its default port. |
| `EXPORT_CLUSTER_CACHE_ENABLED` | `false` | Exports cluster cache data to a file. | Sharing cluster cache with another process. | Usually leave disabled. |
| `EXPORT_CLUSTER_INFO_ENABLED` | `false` | Exports cluster info data to a file. | Sharing cluster info with another process. | Usually leave disabled. |
| `CLUSTER_INFO_FILE_ENABLED` | `false` | Reads cluster info from file instead of local discovery. | Using file-based cluster info. | Requires files under `CONFIG_PATH`. |
| `PRICING_CONFIGMAP_NAME` | `pricing-configs` | Name of the pricing ConfigMap. | Your manifests use a custom ConfigMap name. | Keep aligned with Helm/manifests. |
| `METRICS_CONFIGMAP_NAME` | `metrics-config` | Name of the metrics ConfigMap. | Your manifests use a custom ConfigMap name. | Keep aligned with Helm/manifests. |
| `EMIT_POD_ANNOTATIONS_METRIC` | `false` | Emits pod annotation metrics. | You need pod annotations in exported metrics. | Can increase metric cardinality. |
| `EMIT_NAMESPACE_ANNOTATIONS_METRIC` | `false` | Emits namespace annotation metrics. | You need namespace annotations in exported metrics. | Can increase metric cardinality. |
| `EMIT_DEPRECATED_METRICS` | `false` | Emits deprecated metrics. | Maintaining old dashboards temporarily. | Avoid for new deployments. |
| `EMIT_KSM_V1_METRICS` | `true` | Emits compatibility metrics removed in kube-state-metrics v2. | Supporting dashboards that expect KSM v1 metrics. | Default keeps compatibility. |
| `EMIT_KSM_V1_METRICS_ONLY` | `false` | Emits only KSM v1 compatibility metrics. | Running a specialized compatibility mode. | Avoid unless explicitly needed. |
| `METRICS_EMITTER_QUERY_WINDOW` | `2m` | Historical query window for metrics emitter cost data queries. | Metrics emission needs a wider/narrower query lookback. | Use duration strings like `5m`. Wider windows can increase query cost. |

## Prometheus

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `PROMETHEUS_SERVER_ENDPOINT` | empty | Prometheus-compatible query endpoint. | Always, when using Prometheus-backed OpenCost. | In HA/sharded setups, point to Thanos, Mimir, Cortex, or another global query endpoint. |
| `PROMETHEUS_QUERY_TIMEOUT` | `120s` | Timeout for Prometheus queries. | Queries time out under normal load. | Increase carefully; long timeouts can tie up workers. |
| `PROMETHEUS_KEEP_ALIVE` | `120s` | HTTP keep-alive duration for Prometheus clients. | Tuning Prometheus client networking. | Usually leave default. |
| `PROMETHEUS_TLS_HANDSHAKE_TIMEOUT` | `10s` | TLS handshake timeout for Prometheus clients. | Prometheus TLS handshakes are slow. | Usually leave default. |
| `PROMETHEUS_QUERY_RESOLUTION_SECONDS` | `300` | Query resolution in seconds. | You need more precision or lower Prometheus load. | Lower values are more precise but more expensive. |
| `PROMETHEUS_MAX_QUERY_DURATION_MINUTES` | `1440` | Max duration per Prometheus query chunk. | Large windows overload Prometheus. | Default limits chunks to 1 day. |
| `MAX_QUERY_CONCURRENCY` | `5` | Max concurrent Prometheus queries. | Tuning load against Prometheus. | If `<=0`, falls back to `GOMAXPROCS`. Keep modest for shared Prometheus. |
| `KUBECOST_SCRAPE_INTERVAL` | `0` | Optional scrape interval override. | Prometheus scrape interval is known and should be explicit. | Use duration strings such as `60s`. |
| `KUBECOST_JOB_NAME` | `kubecost` | Prometheus job label value for OpenCost metrics. | Your scrape job uses a different name. | Keep aligned with Prometheus scrape config. |
| `PROM_CLUSTER_ID_LABEL` | `cluster_id` | Prometheus label used for cluster ID. | Your metrics use a different cluster label. | Must match the labels in Prometheus. |
| `CURRENT_CLUSTER_ID_FILTER_ENABLED` | `false` | Adds a current-cluster filter to Prometheus queries. | Prometheus contains data from multiple clusters. | Requires `CLUSTER_ID` and `PROM_CLUSTER_ID_LABEL` to be correct. |
| `PROMETHEUS_HEADER_X_SCOPE_ORGID` | empty | Adds `X-Scope-OrgID` for Mimir/Cortex tenant routing. | Querying multi-tenant Mimir/Cortex. | Set to the tenant/org ID. |
| `PROMETHEUS_RETRY_ON_RATE_LIMIT` | `true` | Retries Prometheus requests on rate-limit responses. | Using rate-limited Prometheus-compatible backends. | Keep enabled for AMP and similar services. |
| `PROMETHEUS_RETRY_ON_RATE_LIMIT_MAX_RETRIES` | `5` | Max retries for rate-limited Prometheus requests. | Tuning retry behavior. | Higher values increase latency under throttling. |
| `PROMETHEUS_RETRY_ON_RATE_LIMIT_DEFAULT_WAIT` | `100ms` | Default wait before retry when no retry header is provided. | Tuning retry backoff. | Use duration strings. |
| `INSECURE_SKIP_VERIFY` | `false` | Skips TLS verification for Prometheus connections. | Testing with self-signed certificates. | Avoid in production. Prefer proper CA configuration. |
| `KUBE_RBAC_PROXY_ENABLED` | `false` | Enables kube-rbac-proxy handling for Prometheus. | Prometheus access is protected by kube-rbac-proxy. | Configure together with cluster RBAC. |
| `DB_BASIC_AUTH_USERNAME` | empty | Basic auth username for Prometheus-compatible backend. | Backend requires basic auth. | Store as a secret. |
| `DB_BASIC_AUTH_PW` | empty | Basic auth password for Prometheus-compatible backend. | Backend requires basic auth. | Store as a secret. |
| `DB_BEARER_TOKEN` | empty | Bearer token for Prometheus-compatible backend. | Backend requires bearer auth. | Store as a secret. |
| `PROM_MTLS_AUTH_CA_FILE` | empty | CA file for Prometheus mTLS auth. | Backend requires mTLS. | mTLS is enabled only when CA, cert, and key files are all set. |
| `PROM_MTLS_AUTH_CRT_FILE` | empty | Client certificate file for Prometheus mTLS auth. | Backend requires mTLS. | Mount from a secret. |
| `PROM_MTLS_AUTH_KEY_FILE` | empty | Client key file for Prometheus mTLS auth. | Backend requires mTLS. | Mount from a secret. |

## API Performance

For more detail, see [API Performance Environment Variables](api-performance-env-vars.md).

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `OPENCOST_QUERY_CACHE_TTL_SECONDS` | `60` | Global query response cache baseline. | Repeated API requests should avoid recompute. | Set `0` to disable response cache. For dashboards, use `300`. |
| `OPENCOST_REALTIME_QUERY_CACHE_TTL_SECONDS` | `30` | Response cache TTL for windows ending within the last hour. | Frontend uses relative windows like `window=7d`. | Set at least equal to warmer interval, for example `300` or `360`. |
| `OPENCOST_HISTORICAL_QUERY_CACHE_TTL_SECONDS` | `300` | Response cache TTL for historical windows. | Historical API views are used frequently. | `600` is a reasonable dashboard value. |
| `OPENCOST_ALLOCATION_STEP_CACHE_TTL_SECONDS` | `30` | Internal realtime allocation step cache TTL. | Allocation-derived endpoints are repeatedly queried. | Pair with realtime query cache TTL. |
| `OPENCOST_ALLOCATION_HISTORICAL_STEP_CACHE_TTL_SECONDS` | `600` | Internal historical allocation step cache TTL. | Historical allocation queries are repeated. | `600` to `1800` is usually safe if data backfill is uncommon. |
| `OPENCOST_CACHE_WARMER_ENABLED` | `false` | Enables background warming of common read-only API responses. | First dashboard request is slow. | Recommended `true` for frontend-heavy deployments. |
| `OPENCOST_CACHE_WARMER_INTERVAL_SECONDS` | `300` | Interval between cache warmer runs. | Cache warmer is enabled. | Keep less than or equal to realtime response cache TTL. |
| `OPENCOST_CACHE_WARMER_STARTUP_DELAY_SECONDS` | `30` | Delay before first cache warm after startup. | Startup dependencies need time to become ready. | Use `5` to `30`. Longer if Prometheus is slow at startup. |
| `OPENCOST_CACHE_WARMER_ENDPOINTS` | built-in defaults | Semicolon-separated read-only endpoints to warm. | Frontend uses specific query shapes. | Match actual frontend query strings. Do not include mutating endpoints. |

## Allocation, Assets, And CSV Export

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `INGEST_POD_UID` | `false` | Includes pod UID in allocation keys. | Pod name reuse causes allocation ambiguity. | Changes allocation key shape; test dashboards before enabling. |
| `ALLOCATION_NODE_LABELS_ENABLED` | `true` | Includes node labels in allocation metadata. | You need node label dimensions. | Disable only to reduce metadata/cardinality. |
| `ASSET_INCLUDE_LOCAL_DISK_COST` | `true` | Includes local disk costs in asset results. | You need to exclude local disk from asset cost. | Usually keep enabled. |
| `REGION_OVERRIDE_LIST` | empty | Comma-separated region override list. | Provider region data needs override handling. | Provider-specific; keep empty unless needed. |
| `EXPORT_CSV_FILE` | empty | File path for periodic allocation CSV export. | You need CSV export generated by the service. | Empty disables CSV export. Ensure path is writable. |
| `EXPORT_CSV_LABELS_LIST` | empty | Comma-separated allocation labels to include in CSV. | CSV export should include selected labels. | Used only when CSV export is enabled. |
| `EXPORT_CSV_LABELS_ALL` | `false` | Includes all allocation labels in CSV. | CSV export needs all labels. | Can create large CSVs. |
| `EXPORT_CSV_MAX_DAYS` | `90` | Max lookback days for CSV export. | You need shorter/longer CSV export history. | Larger values increase compute and file size. |
| `CARBON_ESTIMATES_ENABLED` | `false` | Enables carbon estimate endpoints/routes. | Carbon estimate data is required. | Leave disabled unless frontend uses carbon endpoints. |

## Cloud Provider Selection And Pricing

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `CLOUD_PROVIDER` | empty | Explicitly selects cloud provider. | Auto-detection is wrong or unavailable. | Use provider names expected by provider code/config. |
| `CLOUD_PROVIDER_API_KEY` | empty | Generic cloud provider API key. | Provider integration requires a generic API key. | Also used as fallback for DigitalOcean token. Store as a secret. |
| `USE_CSV_PROVIDER` | `false` | Enables CSV-based pricing provider. | Pricing is sourced from CSV. | Configure CSV variables below. |
| `USE_CUSTOM_PROVIDER` | `false` | Enables custom pricing provider. | Pricing is supplied by custom provider config. | Provider-specific. |
| `CSV_REGION` | empty | Region for CSV pricing provider. | Using CSV provider. | Must match CSV contents. |
| `CSV_ENDPOINT` | empty | S3-compatible endpoint for CSV provider. | CSV pricing is stored in S3-compatible storage. | Use with `CSV_PATH`. |
| `CSV_PATH` | empty | Key/path for CSV pricing provider. | Using CSV provider. | Ensure credentials/config are available to read it. |
| `PROVIDER_PRICING_URL` | provider default | Overrides provider pricing URL for OCI and DigitalOcean. | Air-gapped or mirrored pricing data. | For OCI default is Oracle pricing API; for DigitalOcean default is sizes API. |
| `CLUSTER_PROFILE` | `development` | Cluster profile string. | Deployment distinguishes profiles. | Usually leave default. |

## AWS

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `AWS_ACCESS_KEY_ID` | empty | AWS access key for AWS pricing/cloud integrations. | Static AWS credentials are required. | Prefer IAM roles when possible. Store as a secret. |
| `AWS_SECRET_ACCESS_KEY` | empty | AWS secret access key. | Static AWS credentials are required. | Store as a secret. |
| `AWS_CLUSTER_ID` | empty | AWS-specific cluster ID. | AWS provider needs explicit cluster identity. | Deprecated in favor of `CLUSTER_ID` where possible. |
| `AWS_PRICING_URL` | AWS default | Overrides AWS pricing URL. | Air-gapped or mirrored AWS pricing. | Use only when you maintain compatible pricing data. |
| `AWS_ECS_PRICING_URL` | AWS default | Overrides Amazon ECS pricing URL. | Air-gapped or mirrored ECS pricing. | Use only when needed. |

## Azure

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `AZURE_OFFER_ID` | empty | Azure offer ID for rate card pricing. | Azure pricing requires a specific offer. | Provider-specific. |
| `AZURE_BILLING_ACCOUNT` | empty | Azure billing account for customer-specific pricing. | Using Azure consumption price sheet API. | Requires Azure credentials/config. |
| `AZURE_LOCALE` | `en-US` | Azure rate card locale. | Rate card locale must be changed. | Usually leave default. |
| `AZURE_CURRENCY` | config/default | Azure rate card currency override. | Pricing should be returned in a specific currency. | Overrides config when set. |
| `AZURE_REGION_INFO` | config/default | Azure rate card region override. | Pricing should be constrained to a specific region. | Overrides config when set. |
| `AZURE_DOWNLOAD_BILLING_DATA_TO_DISK` | `true` | Stores Azure billing data on disk instead of only memory. | Controlling Azure billing data storage behavior. | Disk path is under `CONFIG_PATH/db/cloudcost`. Ensure writable storage. |

## Alibaba, DigitalOcean, OCI, And OVH

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `ALIBABA_ACCESS_KEY_ID` | empty | Alibaba Cloud access key. | Alibaba provider needs static credentials. | Store as a secret. |
| `ALIBABA_SECRET_ACCESS_KEY` | empty | Alibaba Cloud secret key. | Alibaba provider needs static credentials. | Store as a secret. |
| `DIGITALOCEAN_ACCESS_TOKEN` | empty | DigitalOcean API token. | DigitalOcean provider needs API access. | Falls back to `CLOUD_PROVIDER_API_KEY` if empty. Store as a secret. |
| `OVH_SUBSIDIARY` | `FR` | OVH subsidiary code for pricing. | OVH pricing should use a different subsidiary. | Value is uppercased and trimmed. |
| `OVH_MONTHLY_NODEPOOLS` | empty | Comma-separated OVH node pools billed monthly. | OVH deployment has monthly node pool billing. | Use exact node pool names. |

## Cloud Cost And Custom Cost

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `CLOUD_COST_ENABLED` | `false` | Enables cloud cost ingestion and APIs. | Cloud cost data is required. | Requires cloud integration config. |
| `CLOUD_COST_MONTH_TO_DATE_INTERVAL` | `6` | Month-to-date interval setting for cloud cost. | Tuning cloud cost MTD behavior. | Integer value; provider-specific operational impact. |
| `CLOUD_COST_REFRESH_RATE_HOURS` | `6` | Cloud cost refresh interval in hours. | Tuning ingestion freshness vs API/provider load. | Lower values increase provider API usage. |
| `CLOUD_COST_QUERY_WINDOW_DAYS` | `7` | Query lookback window in days. | API queries should cover a different default window. | Larger windows can increase query cost. |
| `CLOUD_COST_RUN_WINDOW_DAYS` | `3` | Cloud cost pipeline run window in days. | Backfill or ingestion window needs tuning. | Larger windows increase ingestion cost. |
| `CLOUD_COST_RESOLUTION_1D_RETENTION` | `30` | Daily cloud cost retention in days. | Retention requirements differ. | Uses the prefixed retention fallback pattern. |
| `CUSTOM_COST_ENABLED` | `false` | Enables custom cost ingestion and APIs. | Custom cost plugins/integrations are used. | Requires plugin/config setup. |
| `CUSTOM_COST_QUERY_WINDOW_DAYS` | `7` | Custom cost query window in days. | Custom cost queries need a different window. | Some code paths also read this as hours with default `1`; keep values intentional. |
| `CUSTOM_COST_RESOLUTION_1D_RETENTION` | `30` | Daily custom cost retention in days. | Retention requirements differ. | Uses prefixed retention fallback. |
| `CUSTOM_COST_RESOLUTION_1H_RETENTION` | `49` | Hourly custom cost retention in hours. | Retention requirements differ. | Uses prefixed retention fallback. |
| `PLUGIN_CONFIG_DIR` | `/opt/opencost/plugin/config` | Plugin configuration directory. | Plugin configs are mounted elsewhere. | Must be readable. |
| `PLUGIN_EXECUTABLE_DIR` | `/opt/opencost/plugin/bin` | Plugin executable directory. | Plugin binaries are mounted elsewhere. | Must contain executable plugin binaries. |

## Collector Data Source

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `COLLECTOR_DATA_SOURCE_ENABLED` | `false` | Enables collector-backed data source instead of Prometheus. | Running with collector data source. | Experimental/specialized path. |
| `LOCAL_COLLECTOR_DIRECTORY` | `collector` | Local collector data directory under `CONFIG_PATH`. | Collector data is mounted somewhere else. | Resolved relative to `CONFIG_PATH`. |
| `NETWORK_PORT` | `3001` | Collector network port. | Collector runs on a non-default port. | Collector module setting. |
| `COLLECTOR_SCRAPE_INTERVAL` | `30s` | Collector scrape interval. | Collector scrape cadence differs. | String duration, for example `60s`. |
| `COLLECTOR_RESOLUTION_10M_RETENTION` | `36` | Collector 10-minute retention segments. | Collector retention needs tuning. | Falls back to `RESOLUTION_10M_RETENTION` if set. |
| `COLLECTOR_RESOLUTION_1H_RETENTION` | `49` | Collector hourly retention hours. | Collector retention needs tuning. | Falls back to `RESOLUTION_1H_RETENTION` if set. |
| `COLLECTOR_RESOLUTION_1D_RETENTION` | `15` | Collector daily retention days. | Collector retention needs tuning. | Falls back to `RESOLUTION_1D_RETENTION` if set. |

## Global Retention Fallbacks

These base retention variables are read directly by core helpers and also serve
as fallbacks for prefixed retention variables such as
`COLLECTOR_RESOLUTION_1H_RETENTION`.

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `RESOLUTION_10M_RETENTION` | caller-specific | Number of 10-minute retention segments. | A component uses core retention defaults. | Prefer component-prefixed variables when available. |
| `RESOLUTION_1H_RETENTION` | caller-specific | Number of hourly retention segments. | A component uses core retention defaults. | Prefer component-prefixed variables when available. |
| `RESOLUTION_1D_RETENTION` | caller-specific | Number of daily retention segments. | A component uses core retention defaults. | Prefer component-prefixed variables when available. |

## Node Stats

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `NODESTATS_FORCE_KUBE_PROXY` | `false` | Forces kube-proxy endpoint formatting for node stats. | Node stats direct endpoints are unavailable. | Specialized networking setting. |
| `NODESTATS_LOCAL_PROXY` | empty | Fully qualified local proxy endpoint for node stats. | Node stats should use a local proxy. | Set only when proxy API mode is required. |
| `NODESTATS_INSECURE` | `false` | Skips TLS verification for node stats. | Testing with self-signed kubelet certs. | Avoid in production. |
| `NODESTATS_CERT_FILE` | empty | Client cert file for node stats. | Node stats endpoint requires client cert auth. | Mount from a secret. |
| `NODESTATS_KEY_FILE` | empty | Client key file for node stats. | Node stats endpoint requires client cert auth. | Mount from a secret. |

## Remote Storage And Admin

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `REMOTE_WRITE_ENABLED` | `false` | Enables remote write / SQL-backed persistent storage path. | Using SQL-backed persistent storage. | Requires `SQL_ADDRESS` and credentials/config. |
| `REMOTE_WRITE_PASSWORD` | empty | Password for remote persistent storage. | Remote write storage requires a password. | Store as a secret. |
| `SQL_ADDRESS` | empty | SQL database address for remote persistent storage. | Remote write is enabled. | Ensure network access and credentials. |
| `ADMIN_TOKEN` | empty | Token for admin write operations such as service key or cloud config changes. | Admin endpoints should be protected. | Strongly recommended for any exposed deployment. Store as a secret. |

## MCP Server

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `MCP_SERVER_ENABLED` | `false` | Enables the MCP server. | AI/agent integrations need MCP access. | Disabled by default. Requires Kubernetes mode to be useful. |
| `MCP_HTTP_PORT` | `8081` | MCP server HTTP port. | MCP server needs a non-default port. | Open only to trusted clients. |
| `MCP_QUERY_TIMEOUT_SECONDS` | `60` | Timeout for MCP query operations. | MCP queries need more/less time. | Minimum effective value is `1` second. |

## Telemetry And Reporting

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `LOG_COLLECTION_ENABLED` | `true` | Enables log collection behavior. | Disabling telemetry/log collection. | Set `false` if policy requires it. |
| `PRODUCT_ANALYTICS_ENABLED` | `true` | Enables product analytics behavior. | Disabling product analytics. | Set `false` if policy requires it. |
| `ERROR_REPORTING_ENABLED` | `true` | Enables error reporting behavior. | Disabling error reporting. | Set `false` if policy requires it. |
| `VALUES_REPORTING_ENABLED` | `true` | Enables values reporting behavior. | Disabling values reporting. | Set `false` if policy requires it. |

## Compatibility And Experimental Switches

| Variable | Default | Purpose | Configure when | Notes / recommendation |
|---|---:|---|---|---|
| `USE_CACHE_V1` | `false` | Opts into the older cache path. | Comparing old and new cache behavior. | Temporary compatibility flag. Avoid for new deployments. |

## Provider-Specific Recommended Examples

Prometheus-backed Kubernetes deployment:

```text
PROMETHEUS_SERVER_ENDPOINT=http://prometheus-server.monitoring.svc:9090
CLUSTER_ID=prod-us-east-1
INSTALL_NAMESPACE=opencost
```

Frontend-heavy deployment:

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

Mimir/Cortex tenant query endpoint:

```text
PROMETHEUS_SERVER_ENDPOINT=http://mimir-query-frontend.monitoring.svc/prometheus
PROMETHEUS_HEADER_X_SCOPE_ORGID=<tenant-id>
```

Multi-cluster Prometheus:

```text
CLUSTER_ID=prod-a
PROM_CLUSTER_ID_LABEL=cluster_id
CURRENT_CLUSTER_ID_FILTER_ENABLED=true
```

