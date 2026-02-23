# klog (v5.4+) - 工业级 Kubernetes 可观测性引擎

klog 不仅仅是一个简单的日志查看器，它是一个高度模块化、具备自愈能力和顺序对齐功能的 Kubernetes 异步可观测性系统。

## 🏗 代码架构图

```text
  [ Main / Entry ]
         ↓
  [ Command Layer (cobra) ] ←─── (绑定 Config)
         ↓
  [ Engine (Kernel) ] ──────────┐
         │ (Orchestrator)       │
         ├──────────────────────┴──────────────────────┐
         ↓                      ↓                      ↓
  [ k8s Adapter ]        [ Stream Manager ]     [ Event Bus ]
   (K8s 交互与适配)        (TCP 连接池与自愈)      (异步消息路由)
         ↓                      ↓                      ↓
  [ Module: Logs ] ◄───── [ Module: Trace ] ◄──── [ Future Mods ]
         ↓ (Data Flow)
  [ LogLine Pipeline ]
         ↓
  [ Buffered Sorter ] (200ms 滑动窗口排序)
         ↓
  [ Async Renderer ] (Backpressure 丢包保护输出)
```

## 🚀 核心工作原理

### 1. 引擎驱动模型 (Engine-Driven)
系统完全由一个原子状态机 (`StateInit` -> `Running` -> `Stopping`) 驱动。通过 `Engine` 容器持有所有的子系统句柄，实现了生命周期的统一管理。即使在 TUI 环境下反复重启引擎，底层的 K8s 传输层也会通过 `Provider` 实现单例复用，杜绝泄露。

### 2. 消息驱动解耦 (Event-Driven)
Watcher 发现 Pod 变化后，不再直接通知业务模块，而是将事件推送到 `EventBus`。`LogsModule` 订阅相应的主题进行动态挂载。这种设计使得 `klog` 可以轻松水平扩展 `Metrics`、`Audit` 等其他可观测性插件。

### 3. 分布式排序 (Buffered Selection Sort)
多 Pod 日志乱序是行业痛点。klog 引入了 `PriorityQueue` 和 **200ms 滑动窗口**。日志进入渲染器前会先进入缓冲区，等待窗口期闭合后再由排序器根据原始时间戳弹出。

### 4. 背压与内存安全 (Backpressure)
在高并发日志场景下，渲染速度往往跟不上采集速度。klog 采用了 **Drop-on-Busy (丢弃旧日志)** 策略。
- 缓冲区限制为 2000 行。
- 排序缓冲区堆栈上限 5000 行。
- 超出即丢弃，并记录 `DroppedLines` 指标。

## 📂 项目目录结构说明

- `/cmd`: **控制器层**。负责解析 Flags，挂载 Module 到 Engine 并启动。
- `/internal/engine`: **系统内核**。包含状态机、协程调度、EventBus 和 Module 定义。
- `/internal/k8s`: **适配器层**。封装了 Pod 发现、Client 复用、Informer 监控等 K8s 底层逻辑。
- `/internal/color`: **展示层**。负责高性能的彩色终端打印。
- `/internal/config`: **配置中心**。统一管理所有模块的公共及私有配置。

## 🛡 访问逻辑 (Request Flow)

1. **初始化**：`main` 函数建立 Root Context，截获系统信号。
2. **命令绑定**：`cobra` 解析命令行，将参数注入 `config.Config` 对象。
3. **引擎启动**：`Engine` 被初始化，同时初始化 `Memory Cache` 和 `K8s Provider`。
4. **功能挂载**：根据命令（logs/trace），相应的 `Module` 被 `Attach` 到引擎上。
5. **执行流**：
   - Watcher 将 Pod 事件同步到 `EventBus`。
   - `LogsModule` 监听到 Pod，调用 `StreamManager` 建立连接。
   - 日志流经过 `Pipeline` 进入 `Buffered Sorter`。
   - 排序后的日志进入 `Renderer` 进行异步打印。
6. **优雅关停**：收到 Ctrl+C 后，Root Context 取消，`Engine.Stop()` 逐层通知模块退出，`WaitGroup` 确保所有资源回收。

## 📊 自观测指标
可以通过 `internal/engine` 中的 Stats 查看以下指标：
- **Active Streams**: 当前建立的长连接数。
- **Dropped Lines**: 因为处理积压而被丢弃的日志数。
- **Reconnects**: 自动自愈的重连尝试次数。
