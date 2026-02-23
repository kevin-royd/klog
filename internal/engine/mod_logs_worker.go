package engine

import (
	"sync/atomic"
	"time"

	icolor "klog/internal/color"
)

// streamWorker 生命周期管家：负责单个容器流的长效维护与自愈
func (m *LogsModule) streamWorker(ns, pod, container string) {
	atomic.AddInt32(&m.engine.stats.ActiveStreams, 1)
	defer atomic.AddInt32(&m.engine.stats.ActiveStreams, -1)

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
			// 1. 获取/重建连接 (内部自带 Backoff)
			ls, err := m.engine.Streams().GetOrCreate(ns, pod, container, m.engine.Config())
			if err != nil {
				// 记录重试失败统计
				icolor.Warn("Stream 自愈异常 [%s]: %v, 3秒后重试...", pod, err)
				time.Sleep(3 * time.Second)
				continue
			}

			// 2. 数据处理循环
			m.readLoop(ls)

			// 3. 如果 readLoop 退出且 context 没结束，说明是流异常断开 (如网络抖动)
			select {
			case <-m.ctx.Done():
				return
			default:
				// 记录重连次数
				atomic.AddInt64(&m.engine.stats.ReconnectAttempts, 1)
				icolor.Warn("检测到流中断 [%s], 触发自愈重连...", pod)
				time.Sleep(1 * time.Second) // 基础重连间隔
			}
		}
	}
}

// readLoop 专注于从 Scanner 读取数据并将 LogLine 发往排序器
func (m *LogsModule) readLoop(ls *LogStream) {
	scanner := ls.Scanner
	for scanner.Scan() {
		text := scanner.Text()
		atomic.AddInt64(&m.engine.stats.TotalLines, 1)

		line := &LogLine{
			Raw:           text,
			Namespace:     ls.Namespace,
			PodName:       ls.PodName,
			ContainerName: ls.Container,
			Timestamp:     time.Now(), // 核心：用于排序
		}

		// 执行丢包保护策略 (Backpressure)
		select {
		case <-m.ctx.Done():
			return
		case m.logChan <- line:
			// 处理成功
		default:
			// 缓冲区满，丢弃并计数
			atomic.AddInt64(&m.engine.stats.DroppedLines, 1)
		}
	}
}
