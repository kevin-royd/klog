package trace

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"klog/internal/cache"
	"klog/internal/color"
)

// TracePatterns 常见的Trace ID正则模式
var TracePatterns = []*regexp.Regexp{
	// OpenTelemetry/Jaeger格式: 32位hex
	regexp.MustCompile(`(?i)(?:trace[_-]?id|traceid|x-trace-id|X-B3-TraceId)["\s:=]+([a-f0-9]{32})`),
	// 16位hex trace id
	regexp.MustCompile(`(?i)(?:trace[_-]?id|traceid)["\s:=]+([a-f0-9]{16})`),
	// UUID格式
	regexp.MustCompile(`(?i)(?:trace[_-]?id|traceid|request[_-]?id|correlation[_-]?id)["\s:=]+"?([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})"?`),
	// W3C Trace Context
	regexp.MustCompile(`(?i)traceparent["\s:=]+"?\d{2}-([a-f0-9]{32})-[a-f0-9]{16}-\d{2}"?`),
}

// SpanPatterns Span ID正则
var SpanPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:span[_-]?id|spanid)["\s:=]+"?([a-f0-9]{16})"?`),
}

// TraceSpan 一个跟踪span
type TraceSpan struct {
	TraceID       string
	SpanID        string
	ParentSpanID  string
	ServiceName   string
	PodName       string
	Namespace     string
	ContainerName string
	Timestamp     time.Time
	Duration      time.Duration
	LogLines      []*cache.LogEntry
}

// TraceGroup 一个完整的trace
type TraceGroup struct {
	TraceID   string
	Spans     []*TraceSpan
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	Services  []string
	Pods      []string
}

// Aggregator Trace聚合器
type Aggregator struct {
	mu       sync.RWMutex
	traces   map[string]*TraceGroup
	printer  *color.Printer
	patterns []*regexp.Regexp
}

// NewAggregator 创建trace聚合器
func NewAggregator(printer *color.Printer) *Aggregator {
	return &Aggregator{
		traces:   make(map[string]*TraceGroup),
		printer:  printer,
		patterns: TracePatterns,
	}
}

// SetCustomPatterns 设置自定义trace id正则
func (a *Aggregator) SetCustomPatterns(patterns []*regexp.Regexp) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.patterns = append(a.patterns, patterns...)
}

// ExtractTraceID 从日志行中提取trace ID
func (a *Aggregator) ExtractTraceID(line string) string {
	a.mu.RLock()
	patterns := a.patterns
	a.mu.RUnlock()

	for _, re := range patterns {
		matches := re.FindStringSubmatch(line)
		if len(matches) >= 2 {
			return matches[1]
		}
	}
	return ""
}

// ExtractSpanID 从日志行中提取span ID
func (a *Aggregator) ExtractSpanID(line string) string {
	for _, re := range SpanPatterns {
		matches := re.FindStringSubmatch(line)
		if len(matches) >= 2 {
			return matches[1]
		}
	}
	return ""
}

// AddLogEntry 添加日志条目到trace聚合
func (a *Aggregator) AddLogEntry(entry *cache.LogEntry) {
	traceID := a.ExtractTraceID(entry.Line)
	if traceID == "" {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	entry.TraceID = traceID

	group, ok := a.traces[traceID]
	if !ok {
		group = &TraceGroup{
			TraceID:   traceID,
			StartTime: entry.Timestamp,
		}
		a.traces[traceID] = group
	}

	// 查找或创建span
	spanID := a.ExtractSpanID(entry.Line)
	var span *TraceSpan
	for _, s := range group.Spans {
		if s.PodName == entry.PodName && s.ContainerName == entry.ContainerName {
			span = s
			break
		}
	}

	if span == nil {
		span = &TraceSpan{
			TraceID:       traceID,
			SpanID:        spanID,
			PodName:       entry.PodName,
			Namespace:     entry.Namespace,
			ContainerName: entry.ContainerName,
			Timestamp:     entry.Timestamp,
		}
		group.Spans = append(group.Spans, span)
	}

	span.LogLines = append(span.LogLines, entry)

	// 更新时间范围
	if !entry.Timestamp.IsZero() {
		if entry.Timestamp.Before(group.StartTime) || group.StartTime.IsZero() {
			group.StartTime = entry.Timestamp
		}
		if entry.Timestamp.After(group.EndTime) {
			group.EndTime = entry.Timestamp
		}
	}
}

// GetTrace 获取trace详情
func (a *Aggregator) GetTrace(traceID string) *TraceGroup {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.traces[traceID]
}

// GetAllTraces 获取所有traces
func (a *Aggregator) GetAllTraces() []*TraceGroup {
	a.mu.RLock()
	defer a.mu.RUnlock()

	traces := make([]*TraceGroup, 0, len(a.traces))
	for _, t := range a.traces {
		// 更新汇总信息
		t.Duration = t.EndTime.Sub(t.StartTime)
		serviceSet := make(map[string]bool)
		podSet := make(map[string]bool)
		for _, span := range t.Spans {
			podSet[span.PodName] = true
			if span.ServiceName != "" {
				serviceSet[span.ServiceName] = true
			}
		}
		t.Services = mapKeys(serviceSet)
		t.Pods = mapKeys(podSet)
		traces = append(traces, t)
	}

	// 按开始时间排序
	sort.Slice(traces, func(i, j int) bool {
		return traces[i].StartTime.Before(traces[j].StartTime)
	})

	return traces
}

// PrintTraceSummary 打印trace汇总
func (a *Aggregator) PrintTraceSummary() {
	traces := a.GetAllTraces()

	if len(traces) == 0 {
		color.Info("未发现任何Trace ID")
		return
	}

	color.Header("Trace 汇总 (共 %d 个)", len(traces))
	fmt.Println()

	for _, t := range traces {
		fmt.Printf("  TraceID: %s\n", color.NewPrinter().GetPodColor(t.TraceID).Sprint(t.TraceID))
		fmt.Printf("  时间范围: %s → %s (耗时 %v)\n",
			t.StartTime.Format("15:04:05.000"),
			t.EndTime.Format("15:04:05.000"),
			t.Duration)
		fmt.Printf("  涉及Pod: %s\n", strings.Join(t.Pods, ", "))

		totalLines := 0
		for _, span := range t.Spans {
			totalLines += len(span.LogLines)
		}
		fmt.Printf("  日志行数: %d (跨 %d 个Span)\n", totalLines, len(t.Spans))
		color.Separator()
	}
}

// PrintTraceDetail 打印详细的trace日志
func (a *Aggregator) PrintTraceDetail(traceID string) {
	group := a.GetTrace(traceID)
	if group == nil {
		color.Error("Trace %s 不存在", traceID)
		return
	}

	color.Header("Trace 详情: %s", traceID)
	fmt.Printf("时间范围: %s → %s\n",
		group.StartTime.Format("2006-01-02 15:04:05.000"),
		group.EndTime.Format("2006-01-02 15:04:05.000"))
	fmt.Println()

	// 按时间排序所有日志行
	var allEntries []*cache.LogEntry
	for _, span := range group.Spans {
		allEntries = append(allEntries, span.LogLines...)
	}
	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].Timestamp.Before(allEntries[j].Timestamp)
	})

	for _, entry := range allEntries {
		a.printer.PrintLog(entry.Namespace, entry.PodName, entry.ContainerName, entry.Line)
	}
}

// Clear 清空聚合数据
func (a *Aggregator) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.traces = make(map[string]*TraceGroup)
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
