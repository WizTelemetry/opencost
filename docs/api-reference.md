# `/kapis/costwise.wiztelemetry.io/v1alpha1` API 接口文档

> 规范路径前缀，前端集成的 canonical API contract。
> Runtime 同时提供不带前缀的兼容路由，但文档仅展示 prefixed path。

---

## 目录

- [通用概念](#通用概念)
- [1. Allocation - 成本分配](#1-allocation---成本分配)
- [2. Asset - 资产数据](#2-asset---资产数据)
- [3. Efficiency - 效率优化](#3-efficiency---效率优化)
- [附录 A: Filter 语法详解](#附录-a-filter-语法详解)
- [附录 B: 聚合维度速查](#附录-b-聚合维度速查)
- [附录 C: 响应结构](#附录-c-响应结构)

---

## 通用概念

### 时间窗口 (window)

几乎所有数据查询接口都要求 `window` 参数，支持以下格式：

| 格式 | 含义 | 示例 |
|------|------|------|
| 相对时长 | 当前时间往前推 | `window=1h`、`window=24h`、`window=7d`、`window=30d` |
| 自然语义 | 当天/当周 | `window=today`、`window=yesterday`、`window=week`、`window=month` |
| 绝对时间 | 精确 UTC 时间范围 | `window=2026-05-01T00:00:00Z,2026-05-08T00:00:00Z` |

> **注意：** 使用相对窗口（如 `7d`、`24h`）时，系统在同一 5 分钟桶内复用缓存 key，提升命中率。绝对时间窗口不做归一化。

### 聚合维度 (aggregate)

将查询结果按字段分组汇总。支持逗号分隔的多个维度。

**分配维度：** `cluster`、`node`、`namespace`、`controllerKind`、`controller`、`pod`、`container`、`providerID`、`service`、`deployment`、`statefulset`、`daemonset`、`job`、`department`、`environment`、`owner`、`product`、`team`、`label:<key>`、`annotation:<key>`

**资产维度：** `type`、`name`、`cluster`、`provider`、`service`、`category`、`account`、`project`、`providerID`、`label:<key>`

> 不传 aggregate 会返回原始明细。详情见 [附录 B](#附录-b-聚合维度速查)。

### 时间分桶：step vs accumulate

- **`step`** — 固定时长分桶（Go duration 格式：`1h`、`12h`、`24h`）。按固定时长切分窗口。
- **`accumulate`** — 自然时间粒度累积。支持 `hour`、`day`、`week`、`month`、`all`。

> `step` 和 `accumulate` **不能同时使用**。

### 过滤条件 (filter)

使用声明式过滤语法。详情见 [附录 A](#附录-a-filter-语法详解)。

```
filter=cluster:"prod"+namespace:"default"
filter=label[app]:"cost-analyzer"
filter=namespace:"kubecost"|namespace:"default"
```

> URL 中 `+` 建议编码为 `%2B`，避免被解析为空格。

---

## 1. Allocation - 成本分配

### 1.1 `GET /allocation` / `/allocation/compute`

查询成本分配明细数据。支持聚合、过滤、idle、CSV 导出。是分配数据的核心接口。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口。示例：`7d`、`today`、`2026-05-01T00:00:00Z,2026-05-08T00:00:00Z` |
| `aggregate` | string | 否 | — | 聚合维度，逗号分隔。示例：`namespace,label:app` |
| `step` | string | 否 | — | 查询步长。示例：`1d` |
| `accumulate` | string | 否 | — | 累积粒度。支持 `true`、`all`、`hour`、`day`、`week`、`month` |
| `accumulateBy` | string | 否 | — | 显式累积粒度，优先级高于 `accumulate` |
| `filter` | string | 否 | — | 分配过滤条件。详见 [附录 A](#附录-a-filter-语法详解) |
| `includeIdle` | bool | 否 | false | 是否包含 idle 成本（未被工作负载使用的资源成本） |
| `idleByNode` | bool | 否 | false | 是否按节点级别计算 idle |
| `shareIdle` | bool | 否 | false | 是否将 idle 成本按规则分摊回工作负载 |
| `sharelb` | bool | 否 | false | 是否共享负载均衡成本 |
| `includeProportionalAssetResourceCosts` | bool | 否 | false | 是否纳入比例资产资源成本 |
| `includeAggregatedMetadata` | bool | 否 | false | 是否附带聚合元数据 |
| `format` | string | 否 | json | 返回格式；传 `csv` 导出 CSV |

**使用示例：**

```bash
# 按命名空间聚合最近7天成本
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation?window=7d&aggregate=namespace

# 按命名空间+标签聚合，包含idle成本
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation?window=7d&aggregate=namespace,label:app&includeIdle=true

# 按集群过滤生产环境 Pod 成本
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation?window=7d&aggregate=namespace&filter=cluster:"prod"

# CSV 导出
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation?window=7d&aggregate=namespace&format=csv

# 按天累积，观察每日趋势
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation?window=7d&aggregate=namespace&accumulate=day&includeIdle=true

# 按 12 小时步长分桶
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation?window=7d&aggregate=namespace&step=12h
```

### 1.2 `GET /allocation/summary` / `/allocation/compute/summary`

查询分配摘要数据。与 `/allocation` 的区别是返回摘要格式（`SummaryAllocationSet`），适合列表页和汇总视图。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `aggregate` | string | 否 | — | 聚合维度，逗号分隔 |
| `accumulate` | string | 否 | — | 累积粒度。`true`、`all`、`hour`、`day`、`week`、`month` |
| `accumulateBy` | string | 否 | — | 显式累积粒度 |
| `step` | string | 否 | — | 查询步长 |
| `filter` | string | 否 | — | 分配过滤条件 |
| `format` | string | 否 | json | 返回格式；`csv` 导出 CSV |

**使用示例：**

```bash
# 按命名空间聚合，累积整个窗口
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation/summary?window=7d&aggregate=namespace&accumulate=true
```

### 1.3 `GET /allocation/summary/topline`

返回整个查询窗口的 combined total 摘要。适合顶部卡片、总览页指标。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `aggregate` | string | 否 | — | 聚合维度 |
| `filter` | string | 否 | — | 分配过滤条件 |
| `step` | string | 否 | window 大小 | 查询步长 |
| `accumulate` | string | 否 | — | 中间累积粒度。接口始终返回整个窗口的 combined total |
| `idle` | bool | 否 | false | 是否包含 idle 成本 |
| `idleByNode` | bool | 否 | false | 是否按节点计算 idle |
| `shareIdle` | bool | 否 | false | 是否分摊 idle 成本 |

> 返回值结构固定为 `numResults` + `combined.allocations.total`。

**使用示例：**

```bash
# 获取7天总览
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation/summary/topline?window=7d&aggregate=namespace

# 包含idle的总览
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation/summary/topline?window=7d&aggregate=namespace&idle=true&shareIdle=true
```

### 1.4 `GET /allocation/autocomplete`

查询自动补全候选项。前端用于搜索框、筛选器下拉菜单。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `field` | string | **是** | — | 字段名。支持：`cluster`、`node`、`namespace`、`pod`、`container`、`controller`、`controllerKind`、`providerID`、`service`、`label`、`label[<key>]`、`annotation`、`annotation[<key>]` |
| `search` | string | 否 | — | 搜索关键字，按包含过滤候选项 |
| `filter` | string | 否 | — | 分配过滤条件 |

> `node` 返回格式 `cluster/node`，`pod` 返回 `cluster/namespace/pod`，`container` 返回 `cluster/namespace/pod/container`。

**使用示例：**

```bash
# 获取所有命名空间列表
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation/autocomplete?window=7d&field=namespace

# 按关键字搜索标签值
GET /kapis/costwise.wiztelemetry.io/v1alpha1/allocation/autocomplete?window=7d&field=label[app]&search=cost
```

### 1.5 `GET /costDataModel`

返回指定时间窗口内的原始成本数据记录。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `timeWindow` | string | **是** | — | 查询窗口时长（duration 格式）。示例：`24h`、`7d` |
| `offset` | string | 否 | — | 相对当前时间的偏移量。示例：`24h` |
| `filterFields` | string | 否 | — | 仅返回指定字段，逗号分隔 |
| `namespace` | string | 否 | — | 按命名空间过滤 |

---

## 2. Asset - 资产数据

### 2.1 `GET /assets`

查询集群中的原始或聚合资产数据（节点、磁盘、网络、负载均衡器）。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `cluster` | string | 否 | — | 按集群过滤（与 filter 做 AND 合并）。逗号分隔多个 |
| `filter` | string | 否 | — | 资产过滤条件。详见 [附录 A](#a2-资产-asset-filter-字段) |
| `aggregate` | string | 否 | — | 聚合维度。当前仅支持 `type`。不传返回原始 AssetSet |
| `step` | string | 否 | — | 固定时间桶宽度（仅在 `aggregate=type` 时生效） |
| `accumulate` | string | 否 | — | 时间累积方式（仅在 `aggregate=type` 时生效）。`true`、`all`、`day`、`week`、`month` |
| `format` | string | 否 | json | 返回格式；传 `csv` 导出 CSV |

> `step` 和 `accumulate` 互斥。

**使用示例：**

```bash
# 原始资产数据（不聚合）
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets?window=7d

# 按资产类型聚合，整个窗口汇总
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets?window=7d&aggregate=type&accumulate=true

# 按资产类型聚合，按 12h 分桶
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets?window=7d&aggregate=type&step=12h

# 按集群过滤 + 资产类型过滤
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets?window=7d&aggregate=type&accumulate=day&cluster=prod&filter=assetType:"node"
```

### 2.2 `GET /assets/graph`

返回图表端资产数据。按时间分桶、聚合后按成本降序排列，支持分页。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `aggregate` | string | 否 | `type` | 聚合维度。`type`、`name`、`cluster`、`provider`、`service`、`category`、`account`、`project`、`providerID`、`label:<key>` |
| `step` | string | 否 | — | 固定时间桶宽度（与 accumulate 互斥） |
| `accumulate` | string | 否 | `day` | 时间粒度。`hour`、`day`、`week`、`month` |
| `cluster` | string | 否 | — | 按集群过滤（与 filter 做 AND 合并） |
| `filter` | string | 否 | — | 资产过滤条件 |
| `offset` | int | 否 | 0 | 每个时间片跳过前 N 个条目（分页/查看 TopN 之后） |
| `limit` | int | 否 | 25 | 每个时间片返回的最大条目数 |
| `format` | string | 否 | json | 返回格式；传 `csv` 导出 CSV |

> 每个时间片返回 `totalCost`（所有项的总成本）+ `items`（按成本降序排列的聚合项）。

**使用示例：**

```bash
# 按资产类型查看7天趋势
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets/graph?window=7d&aggregate=type

# 按集群查看最近的24小时趋势
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets/graph?window=24h&aggregate=cluster&accumulate=hour

# Top 10 资产类型
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets/graph?window=7d&aggregate=type&limit=10&offset=0

# 按标签查看资产
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets/graph?window=7d&aggregate=label:team&accumulate=week
```

### 2.3 `GET /assets/autocomplete`

查询资产字段自动补全候选项。前端可用于资产筛选器、搜索框下拉菜单。

> 仅从 `node` 和 `disk` 类型资产中提取数据。`node` 返回格式为 `cluster/node`。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `field` | string | **是** | — | 字段名。支持：`cluster`、`node`、`providerID`、`name`、`assetType`、`label`、`label[<key>]` |
| `search` | string | 否 | — | 搜索关键字，按包含关系过滤候选项 |
| `cluster` | string | 否 | — | 按集群过滤（会与 `filter` 做 AND 合并） |
| `filter` | string | 否 | — | 资产过滤条件 |

**使用示例：**

```bash
# 获取资产节点候选项
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets/autocomplete?window=7d&field=node

# 获取特定标签值
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets/autocomplete?window=7d&field=label[team]&search=plat

# 仅在指定集群内获取 providerID
GET /kapis/costwise.wiztelemetry.io/v1alpha1/assets/autocomplete?window=7d&field=providerID&cluster=prod
```

### 2.4 `GET /assets/carbon`

查询资产的碳足迹估算数据。

> **注意：** 此路由仅在 `carbonEnabled=true` 时注册。返回 404 表示当前部署未启用碳排放估算。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `cluster` | string | 否 | — | 按集群过滤 |
| `filter` | string | 否 | — | 资产过滤条件 |

---

## 3. Efficiency - 效率优化

### 3.1 `GET /efficiency`

基于窗口和聚合维度计算资源效率（CPU/内存 usage/request 比）、推荐请求值和潜在成本节省。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `aggregate` | string | 否 | `pod` | 聚合维度 |
| `filter` | string | 否 | — | 分配过滤条件 |
| `bufferMultiplier` | number | 否 | `1.2` | 推荐资源计算缓冲系数（1.2 = 20% 余量，1.4 = 40%） |
| `format` | string | 否 | json | 返回格式；`csv` 导出 CSV |

**使用示例：**

```bash
# 按命名空间计算效率
GET /kapis/costwise.wiztelemetry.io/v1alpha1/efficiency?window=7d&aggregate=namespace

# 更保守的缓冲系数
GET /kapis/costwise.wiztelemetry.io/v1alpha1/efficiency?window=7d&aggregate=namespace&bufferMultiplier=1.4
```

### 3.2 `GET /efficiency/clusters` / `/efficiency/clusters/summary`

基于分配数据计算集群效率摘要。返回 `groups` + `groupBy` 结构。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `window` | string | **是** | — | 时间窗口 |
| `step` | string | 否 | — | 查询步长。未传时按日查询后累积为整个 window |
| `accumulate` | bool | 否 | false | 是否将整个窗口累积为单个结果 |
| `aggregate` | string | 否 | `cluster` | 聚合维度 |
| `filter` | string | 否 | — | 分配过滤条件 |

> 总成本包含 PV 和 idle。efficiency 口径与分配摘要页面一致。

**使用示例：**

```bash
# 按集群查看效率摘要
GET /kapis/costwise.wiztelemetry.io/v1alpha1/efficiency/clusters?window=7d&step=1d&accumulate=true

# 按命名空间查看效率
GET /kapis/costwise.wiztelemetry.io/v1alpha1/efficiency/clusters?window=7d&aggregate=namespace&accumulate=true
```

## 附录 A: Filter 语法详解

### A.1 语法规则

```
<过滤器>     ::= <过滤元素> (<组运算符> <过滤元素>)*
<过滤元素>   ::= <比较式> | <分组>
<分组>       ::= '(' <过滤器> ')'
<组运算符>   ::= '+' (AND) | '|' (OR)
<比较式>     ::= <字段> <运算符> <值>
<值>         ::= '"' 内容 '"' (',' <值>)*
<字段>       ::= 普通字段 | <Map字段>'['键']'
```

### A.2 运算符

| 运算符 | 含义 | 示例 |
|--------|------|------|
| `:` | 等于（slice/map 为 contains） | `namespace:"default"` |
| `!:` | 不等于（slice/map 为 notcontains） | `namespace!:"kube-system"` |
| `~:` | 包含子串 | `pod~:"nginx"` |
| `!~:` | 不包含子串 | `pod!~:"test"` |
| `<~:` | 前缀匹配 | `cluster<~:"prod-"` |
| `!<~:` | 非前缀匹配 | `cluster!<~:"test-"` |
| `~>:` | 后缀匹配 | `cluster~>:"-prod"` |
| `!~>:` | 非后缀匹配 | `cluster!~>:"-staging"` |

### A.3 组合与嵌套

- `+` = AND（同一作用域内不可与 `|` 混用）
- `|` = OR（同一作用域内不可与 `+` 混用）
- `()` = 显式分组（可跨作用域混合 AND/OR）
- 逗号分隔值：正向运算符 OR、负向运算符 AND

```bash
# 简单 AND
filter=cluster:"prod"+namespace:"default"

# 简单 OR
filter=namespace:"kubecost"|namespace:"default"

# 嵌套——在 prod 集群中，只看 kubecost 或 default 命名空间
filter=cluster:"prod"+(namespace:"kubecost"|namespace:"default")

# 多值——排除多个命名空间
filter=namespace!:"kube-system","kube-public","istio-system"

# Map 字段——查询特定标签
filter=label[app]:"cost-analyzer"
filter=annotation[team]:"platform"

# 复杂嵌套
filter=(label[foo]:"bar"+annotation[foo]:"bar")|(label!~:"foo"+annotation~:"foo")
```

### A.4 分配 (Allocation) Filter 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `cluster` | string | 集群 ID |
| `node` | string | 节点名称 |
| `namespace` | string | Kubernetes 命名空间 |
| `controllerName` | string | 控制器名称 |
| `controllerKind` | string | 控制器类型（deployment/statefulset/daemonset/job） |
| `container` | string | 容器名称 |
| `pod` | string | Pod 名称 |
| `provider` | string | 云提供商 |
| `account` | string | 云账号 |
| `services` | slice | 服务名称列表 |
| `label` | map | Pod 标签（支持 `label[key]`） |
| `annotation` | map | Pod 注解（支持 `annotation[key]`） |
| `nodeLabel` | map | 节点标签（支持 `nodeLabel[key]`） |
| `namespaceLabel` | map | 命名空间标签（支持 `namespaceLabel[key]`） |
| `department` | alias | 通过 LabelConfig 映射 |
| `environment` | alias | 通过 LabelConfig 映射 |
| `owner` | alias | 通过 LabelConfig 映射 |
| `product` | alias | 通过 LabelConfig 映射 |
| `team` | alias | 通过 LabelConfig 映射 |

### A.5 资产 (Asset) Filter 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `assetType` | string | 资产类型（Node/Cloud/Disk/Network/LoadBalancer） |
| `name` | string | 资产名称 |
| `category` | string | 分类（Compute/Storage/Network/Management） |
| `cluster` | string | 集群 ID |
| `project` | string | 云项目 |
| `provider` | string | 云提供商 |
| `providerID` | string | 提供商资源 ID |
| `account` | string | 云账号 |
| `service` | string | 云服务 |
| `label` | map | 标签（支持 `label[key]`） |

---

## 附录 B: 聚合维度速查

### B.1 分配聚合维度

| 维度 | 说明 | 示例值 |
|------|------|--------|
| `cluster` | 集群 | `prod-us-east` |
| `node` | 节点 | `ip-10-0-0-1.ec2.internal` |
| `namespace` | 命名空间 | `default`、`kube-system` |
| `controllerKind` | 控制器类型 | `deployment`、`statefulset` |
| `controller` | 控制器（含名） | `deployment:nginx` |
| `pod` | Pod 名称 | `nginx-7b9f8c6d5-abc12` |
| `container` | 容器名称 | `nginx` |
| `providerID` | 云资源 ID | `i-0a1b2c3d4e5f6g7h` |
| `service` | 首个服务名 | `nginx-service` |
| `deployment` | Deployment 名 | `nginx` |
| `statefulset` | StatefulSet 名 | `postgres` |
| `daemonset` | DaemonSet 名 | `fluentd` |
| `job` | Job 名 | `db-migration` |
| `label:<key>` | 按标签值聚合 | `label:app`、`label:team` |
| `annotation:<key>` | 按注解值聚合 | `annotation:owner` |

> 缺失值显示为 `__unallocated__`，未挂载资源显示为 `__unmounted__`。

### B.2 资产聚合维度

| 维度 | 说明 | 示例值 |
|------|------|--------|
| `type` | 资产类型 | `Node`、`Disk`、`Network` |
| `name` | 资产名称 | `ip-10-0-0-1` |
| `cluster` | 集群 | `prod-us-east` |
| `provider` | 云提供商 | `aws`、`gcp`、`azure` |
| `service` | 云服务 | `AmazonEC2`、`AmazonS3` |
| `category` | 类别 | `Compute`、`Storage` |
| `account` | 账号 | `123456789012` |
| `project` | 项目 | `my-project` |
| `providerID` | 提供商 ID | `i-0a1b2c3d` |
| `label:<key>` | 按标签值聚合 | `label:team` |

---

## 附录 C: 响应结构

### C.1 标准响应格式

大多数接口返回统一的 `Response` 结构：

```json
{
  "code": 200,
  "data": { ... },
  "message": "success",
  "status": "success",
  "warning": ""
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `code` | int | HTTP 状态码 |
| `data` | any | 业务数据 |
| `message` | string | 响应消息 |
| `status` | string | 状态标识 |
| `warning` | string | 警告信息 |

### C.2 Allocation 数据字段

返回数据中每个分配条目包含（部分关键字段）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 条目名称（按聚合维度生成） |
| `start` / `end` | string | 时间窗口起止 |
| `cpuCoreHours` | float | CPU 核·小时 |
| `cpuCost` | float | CPU 成本 |
| `cpuEfficiency` | float | CPU 效率（usage/request） |
| `ramByteHours` | float | 内存字节·小时 |
| `ramCost` | float | 内存成本 |
| `ramEfficiency` | float | 内存效率（usage/request） |
| `gpuHours` | float | GPU 小时 |
| `gpuCost` | float | GPU 成本 |
| `networkCost` | float | 网络成本 |
| `loadBalancerCost` | float | 负载均衡成本 |
| `pvCost` | float | 持久卷成本 |
| `totalCost` | float | 总成本 |
| `totalEfficiency` | float | 综合效率 |
| `properties` | object | 元数据（labels、annotations、namespace 等） |

### C.3 Asset 数据字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 资产名称 |
| `assetType` | string | 资产类型 |
| `category` | string | 分类 |
| `properties` | object | 资产属性（provider、account 等） |
| `start` / `end` | string | 窗口起止 |
| `totalCost` | float | 总成本 |
| `labels` | object | 资产标签 |
| `adjustment` | float | 调整金额 |

### C.4 Assets/graph 响应格式

```json
{
  "code": 200,
  "data": [
    {
      "window": { "start": "...", "end": "..." },
      "totalCost": 123.45,
      "items": [
        { "name": "Node", "totalCost": 50.0 },
        { "name": "Disk", "totalCost": 30.0 }
      ]
    }
  ]
}
```

每个时间片包含：
- `window` — 该时间片的起止时间
- `totalCost` — 该时间片所有项的总成本
- `items` — 按成本降序排列的聚合项列表

### C.5 Efficiency 响应格式

返回 `efficiency`、`cpuEfficiency`、`ramEfficiency`、推荐值 (`recommendedCPU`、`recommendedRAM`) 和潜在节省 (`potentialCPUSavings`、`potentialRAMSavings`)。

| 字段 | 类型 | 说明 |
|------|------|------|
| `efficiency` | float | 综合效率（0-1） |
| `cpuEfficiency` | float | CPU 效率 |
| `ramEfficiency` | float | 内存效率 |
| `recommendedCPU` | float | 推荐 CPU 请求值（cores） |
| `recommendedRAM` | float | 推荐内存请求值（bytes） |
| `potentialCPUSavings` | float | CPU 潜在节省 |
| `potentialRAMSavings` | float | 内存潜在节省 |

### C.6 错误码

| 状态码 | 含义 |
|--------|------|
| 200 | 成功 |
| 400 | 参数无效（window、aggregate 等格式错误） |
| 404 | 路由不存在（例如 `/assets/carbon` 未启用 carbon） |
| 500 | 内部错误（Prometheus 查询失败、数据处理异常等） |
