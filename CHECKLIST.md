# klog Production Readiness Checklist 🚀

## 1. 引擎稳定性 (Engine Stability)
- [ ] **Start/Stop 幂等性**: `e.Start()` 和 `e.Stop()` 连续调用不应产生 Panic。
- [ ] **协程归零**: `Stop()` 之后，`pprof` 监控中的 goroutine 数应回到基准线。
- [ ] **状态锁**: 必须通过原子操作 (`atomic.CAS`) 保护 Engine 状态切换。

## 2. 集群弹性 (Cluster Resilience)
- [ ] **退避重连**: 所有的 TCP Streams 必须具备指数退避 (Backoff) 逻辑。
- [ ] **429 保护**: 建立速率限制或延迟启动机制，防止瞬间冲垮 K8s API Server。
- [ ] **自愈监测**: `StreamManager` 应自动检测 EOF 并向 `EventBus` 发送重连请求。

## 3. 内存与性能安全 (Resource Safety)
- [ ] **背压保障**: 缓冲区满时必须执行 **Drop-on-Busy**，严禁无限增长堆内存。
- [ ] **异步渲染**: 磁盘 I/O 或 Stdout 慢速不应导致 K8s 网络栈解析停顿。

## 4. 数据准确性 (Data Integrity)
- [ ] **时间轴聚合**: 多 Pod 场景下应提供 200ms-500ms 的排序窗口。
- [ ] **重复过滤**: 自动处理重连期间可能产生的微量重复日志。

## 5. 模块准则 (Module Contract)
- [ ] **解耦通信**: Watcher 不得直接调用 Module 接口，必须通过 `EventBus`。
- [ ] **独立上下文**: 每个 Module 运行在自己的衍生 Context 之下。
