# klog 系统架构说明 (V3)

## 核心哲学
klog 是一个 **Engine-Driven** 的可观测性系统，而非简单的 CLI 工具。

## 系统生命周期 (The Life Loop)
1. **Root Context**: 唯一源自 `main.go`，通过信号拦截器控制。
2. **Engine**: 系统 Owner，拥有所有子系统的生命周期。
3. **Graceful Stop**: `Engine.Stop()` 保证所有 goroutine 清零。

## 模块边界 (The Boundaries)
- **cmd/**: 仅作为 **Controller**。禁止包含业务逻辑，禁止直接调用 K8s SDK。
- **internal/engine/**: **系统内核 (Kernel)**。负责调度 Watcher 事件、管理 StreamPool、驱动 Pipeline。
- **internal/k8s/**: **适配器 (Adapter)**。封装集群通信，对 Engine 屏蔽 API 细节。
- **internal/domain/**: **业务领域模型**。定义什么是 LogLine, 什么是 Trace。

## 所有权准则 (Ownership)
- **StreamManager**: 唯一拥有日志长连接的实体。任何子模块不得直接 close 流。
- **Pipeline**: 只负责处理逻辑，不负责输出。
- **Engine**: 负责在 Watcher 事件和 StreamManager 之间做路由。

## Context 准则
- **禁止使用 `context.Background()`** (除 main 函数外)。
- 所有异步任务必须绑定到 `e.ctx`。
