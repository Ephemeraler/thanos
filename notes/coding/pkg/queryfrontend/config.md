## 垂直查询分片
### 为什么
当前 Thanos 查询执行模型在大规模查询时扩展性不佳, 因此我们提出了一个查询分片算法(query sharding algorithm), 它将查询任务分发到多个 Thanos Querier上.
算法按照 series 进行垂直分片, 这与 Frontend 中已有的水平分片(根据时间范围拆分)机制时互补的. 尽管水平分片对于分发长时间范围的查询(如12小时或更长)
很有帮助, 但是在面对高基数指标时, 即使时短时间范围的查询也可能出现性能问题.

垂直查询分片能将大型查询拆分为多个互不重叠的数据集, 这些数据集可以被并行的检索和处理, 从而降低对单个 Querier 节点资源的依赖, 并使我们能够在读路径上实现更有效的任务调度.

### 当前解决方案的陷阱
当前执行一个 PromQL 查询时, Thanos Querier 会从所有下游 Store 拉取 series 到内存中, 然后将这些数据交给 Prometheus 查询引擎处理. 当某个查询需要拉取大量时间序列(series)时，就会导致 Querier 内存使用非常高. 结果是：即使有多个 Querier 可用，查询工作仍然集中在单个 Querier 上，无法被有效地分发出去.

单个 Querier 会成为性能瓶颈，特别是在处理高基数、高负载查询时，扩展性不佳.

### 目标
- Must: 支持将聚合表达式按用户提供的分片因子 N 拆分为 N 个查询执行;
- Must: 支持回退, 当遇到无法分片的查询或 Querier 时, 应自动退回为单个查询执行;
- Could: 推动构建一个通用的查询分片框架，使其适用于任意查询;
- Should not: 不应修改当前的查询请求路径(即：查询在 Queriers 中的调度方式保持不变)

### 目标受众
在大规模运行 Thanos 的用户，希望通过优化 Thanos 的读路径来提升系统稳定性.

### 如何实现
#### 查询分片算法
查询分片算法利用了 PromQL 表达式中提供的 `grouping labels`, 可以适用于大多数以一个或多个分组标签进行聚合的 PromQL 查询.

具体地, 举例如下:

```promql
sum by (pod) (memory_usage_bytes)
```

该查询在以下 series 集合上执行:
```pgsql
memory_usage_bytes{pod="prometheus-1", region="eu-1", role="apps"}
memory_usage_bytes{pod="prometheus-1", region="us-1", role="infra"}
memory_usage_bytes{pod="prometheus-2", region="eu-1", role="apps"}
memory_usage_bytes{pod="prometheus-2", region="us-1", role="infra"}
```

这个查询会按 `pod` 分组, 对分组内的 series 求和, 相当于执行以下两个查询的并集:
```promql
sum by (pod) (memory_usage_bytes{pod="prometheus-1"})
```
和
```promql
sum by (pod) (memory_usage_bytes{pod="prometheus-2"})
```

因此，我们可以将这两个子查询分别分配到不同的查询分片（Query Shard）中并行执行，最后将结果合并返回给用户.

通过分析 PromQL 中的分组标签，可以将大查询拆成多个小查询并行执行，从而提升查询性能与系统可扩展性.

#### 动态时间序列分区
Thanos Querier 是不知道下游 Store 中有哪些 series 的, 因此它无法轻易的将一个聚合查询拆分成两个互不重叠的子查询.
不过, Querier 可以向 Store 节点传递分片信息(shared info), 告诉它们: 只返回某些 Selector 匹配的时间序列中属于某个分片的那一部分数据.
为实现这个目标, 每个查询分片需要传递一下信息给 Store:

- 总分片数
- 当前所处的分片索引
- 在 PromQL 表达式中发现的分组标签

然后, Store 节点会根据每条时间序列的分组标签做一次哈希取模(hashmod), 并只返回其哈希值模上 shard 总数等于当前 shard index 的那些序列.

以之前例子为例:

查询
```promql
sum by (pod) (memory_usage_bytes)
```

假设我们总共分成 2 个查询分片, 并且唯一的分组标签是 pod. 那么对于每条序列, 我们对 pod 的值做哈希取模:
```text
# hash("pod=prometheus-1") mod 2 = 8848352764449055670 mod 2 = 0
memory_usage_bytes{pod="prometheus-1", region="eu-1", role="apps"}

# hash("pod=prometheus-1") mod 2 = 8848352764449055670 mod 2 = 0
memory_usage_bytes{pod="prometheus-1", region="us-1", role="apps"}

# hash("pod=prometheus-2") mod 2 = 14949237384223363101 mod 2 = 1
memory_usage_bytes{pod="prometheus-2", region="eu-1", role="apps"}

# hash("pod=prometheus-2") mod 2 = 14949237384223363101 mod 2 = 1
memory_usage_bytes{pod="prometheus-2", region="us-1", role="apps"}
```

因此, 前两条序列会落到第一个分片中, 后两条序列会落到第二个分片中.

每个 Querier 执行只处理自己负责的那部分序列, 最后查询分片组件会将各个子查询的结果合并并返回给用户.

#### 启动查询分片
Frontend 已经具备水平分片的查询拆分与合并的能力. 为了提供更好的用户体验, 并避免强制用户部署额外的组件, 我们建议在 Query Frontend 中添加一个新的中间件(middleware)，在现有的"时间对齐（step-alignment）"和"时间切片（horizontal time-slicing）"步骤之后，执行本文提出的垂直查询分片算法.
这样, 用户就可以基于已按时间切片的查询, 再进一步使用垂直分片来限制查询的最大复杂度. 唯一新增的用户参数是: 将 PromQL 聚合表达式拆分成多少个分片(shard 数量)

将垂直查询分片整合进 Frontend 还带来以下额外好处:

- 可以对即时查询(Instant queries)进行分片, 进而支持告警(alerting)和规则(recording rules)分布式执行
- 可以复用现有的查询缓存机制
- 在时间已经切片的基础上再进行垂直分片, 能够有效降低每个查询分片的标签基数(cardinality), 进一步提升查询性能.

![alt text](https://thanos.io/tip/img/vertical-sharding.png)

**Slack 上有人说用 load balancer, 或者 k8s service's cluster ip**

### 缺点与异常
并非所有查询都能轻松地进行分片, 对于某些无法安全分片的聚合操作, 分片器可以简单地退回到将表达式作为单一查询完整执行. 这些情况包括在 PromQL 查询执行过程中使用诸如 label_replace 和 label_join 之类会动态创建新标签的函数. 由于这些标签是在查询执行时任意生成的, 各个存储节点（Stores）对此并不知情, 因此无法在分片匹配系列集时将其考虑在内.

`#TODO` 提及通过 promql 解析/转换来增加可分片查询的数量

分片查询对块检索的影响

鉴于查询是基于系列(series)而非时间进行分片, 对于给定的 leaf 节点, 每个独立分片的大多数指标很可能都存储在相同的块(block)中. 这会使得从某个块中检索 series 的成本按分片数 N 倍增长, 因为该块必须被遍历 N 次.

在我们的分片查询基准测试中, 我们发现对 Store Gateway 和 Receiver 的 Series 调用数量增加并未导致显著的延迟上升. 然而, 我们确实注意到 Prometheus 的远程读取(remote-read)延迟有所增加, 这是由于同一次 Series 调用需要多次执行远程读取. 这是因为分片操作必须在 sidecar 内进行, 即在检索到查询的所有匹配系列之后才执行. 一项类似的关注已经在 Prometheus 社区中提出，建议在 Prometheus TSDB 中原生支持系列分片(见 https://github.com/prometheus/prometheus/issues/10420).

我们在 Store Gateway 中识别到的另一个问题是分片查询对 postings 和块(chunk)查找的影响. 每当 Store Gateway 收到 Series 调用时, 它会首先(从缓存中)检索给定标签匹配器的 postings, 合并这些 postings 并确定需要流式传输哪些块/数据块以满足该查询. 分片可能带来的潜在问题是过多的索引查找以及同一数据块被重复下载多次. 目前尚未对这类峰值场景进行测量, 因此我们无法确定其具体影响.

### 进一步改进计划
未来, 可以对 Thanos 进行额外优化, 使 TSDB 块在压缩(compaction)过程中后台分片. 每个块可被拆分成若干更小的子块, 并附加各自的 shard_id 标签. 这样, Store Gateway 在查询时就能直接利用该标签, 从而避免对每个系列(series)进行 hashmod 计算.