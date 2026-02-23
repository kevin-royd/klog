# klog Engine 开发准则 (V5+)

## 1. 协程所有权 (Goroutine Ownership)
- **绝对禁止** 在 Module 或 Subsystem 中直接使用 `go func() {}()`.
- **必须使用** `e.Spawn(func(ctx context.Context))` 派生。
- **目标**: 确保 `Engine.Stop()` 之后，`sync.WaitGroup` 能保证所有后台任务归零。

## 2. Context 链路机制
- **Root Context**: 归属于 `main.go`。
- **Sub-Context**: 所有的 Stream 或 Module 必须派生自 `e.ctx`。
- **禁止使用**: 全项目禁止 `context.Background()` 和 `context.TODO()`。

## 3. 消息驱动准则 (Eventing)
- **解耦**: Watcher 不允许持有 Module 引用。
- **发布**: Watcher 仅向 `EventBus` 发布 `added/deleted` 事件。
- **订阅**: Module 必须在 `Init()` 阶段通过 `e.Events().Subscribe()` 建立监听，并负责处理 `Backpressure`。

## 4. 连接自愈 (Resilience)
- **退避机制**: 连接 K8s Stream 必须实现 `Exponential Backoff` (建议 100ms-5s)。
- **所有权**: `StreamManager` 是 TCP 链接的唯一 Owner。Module 只能通过它获取流。

## 5. 渲染背压 (Backpressure)
- **Renderer**: 必须是单协程异步运行。
- **缓冲区**: 必须是有界 Channel (默认 2000 行)。
- **溢出策略**: 触发 `Drop-on-Busy`，并原子递增 `stats.DroppedLines`。

## 6. 状态机规范
- **Engine**: 必须通过 `atomic.Int32` 的 State 保护 `Start/Stop`。
- **幂等性**: 连续调用 `Stop()` 或在未启动时调用 `Stop()` 必须是安全的。
