package color

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/fatih/color"
)

// Printer 彩色输出打印器
type Printer struct {
	mu         sync.Mutex
	podColors  map[string]*color.Color
	colorPool  []*color.Color
	colorIndex int
	keywords   []string
}

// NewPrinter 创建彩色输出打印器
func NewPrinter() *Printer {
	return &Printer{
		podColors: make(map[string]*color.Color),
		colorPool: []*color.Color{
			color.New(color.FgCyan),
			color.New(color.FgGreen),
			color.New(color.FgYellow),
			color.New(color.FgBlue),
			color.New(color.FgMagenta),
			color.New(color.FgHiCyan),
			color.New(color.FgHiGreen),
			color.New(color.FgHiYellow),
			color.New(color.FgHiBlue),
			color.New(color.FgHiMagenta),
			color.New(color.FgHiRed),
			color.New(color.FgHiWhite),
		},
	}
}

// SetKeywords 设置高亮关键词
func (p *Printer) SetKeywords(keywords []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keywords = keywords
}

// GetPodColor 获取 Pod 对应的颜色（一致性哈希）
func (p *Printer) GetPodColor(podName string) *color.Color {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.podColors[podName]; ok {
		return c
	}

	h := fnv.New32a()
	h.Write([]byte(podName))
	idx := int(h.Sum32()) % len(p.colorPool)
	c := p.colorPool[idx]
	p.podColors[podName] = c
	return c
}

// PrintLog 打印带颜色的日志行
func (p *Printer) PrintLog(namespace, podName, containerName, line string) {
	podColor := p.GetPodColor(podName)

	// 构建前缀
	prefix := podColor.Sprintf("[%s/%s/%s]", namespace, podName, containerName)

	// 日志级别着色
	line = p.colorizeLogLevel(line)

	// 关键词高亮
	line = p.highlightKeywords(line)

	fmt.Printf("%s %s\n", prefix, line)
}

// PrintLogSimple 简单打印（无namespace）
func (p *Printer) PrintLogSimple(podName, line string) {
	podColor := p.GetPodColor(podName)
	prefix := podColor.Sprintf("[%s]", podName)
	line = p.colorizeLogLevel(line)
	line = p.highlightKeywords(line)
	fmt.Printf("%s %s\n", prefix, line)
}

// colorizeLogLevel 日志级别着色
func (p *Printer) colorizeLogLevel(line string) string {
	upper := strings.ToUpper(line)

	switch {
	case strings.Contains(upper, "ERROR") || strings.Contains(upper, "FATAL") || strings.Contains(upper, "PANIC"):
		return color.RedString("%s", line)
	case strings.Contains(upper, "WARN"):
		return color.YellowString("%s", line)
	case strings.Contains(upper, "DEBUG") || strings.Contains(upper, "TRACE"):
		return color.HiBlackString("%s", line)
	case strings.Contains(upper, "INFO"):
		return line // INFO 保持原色
	default:
		return line
	}
}

// highlightKeywords 关键词高亮
func (p *Printer) highlightKeywords(line string) string {
	p.mu.Lock()
	keywords := make([]string, len(p.keywords))
	copy(keywords, p.keywords)
	p.mu.Unlock()

	highlight := color.New(color.BgYellow, color.FgBlack).SprintFunc()

	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		line = strings.ReplaceAll(line, kw, highlight(kw))
	}
	return line
}

// --- 便捷函数 ---

var (
	errorColor   = color.New(color.FgRed, color.Bold)
	warnColor    = color.New(color.FgYellow, color.Bold)
	infoColor    = color.New(color.FgCyan)
	successColor = color.New(color.FgGreen, color.Bold)
	headerColor  = color.New(color.FgHiWhite, color.Bold, color.Underline)
)

// Error 打印错误信息
func Error(format string, a ...interface{}) {
	errorColor.Printf("✗ "+format+"\n", a...)
}

// Warn 打印警告信息
func Warn(format string, a ...interface{}) {
	warnColor.Printf("⚠ "+format+"\n", a...)
}

// Info 打印普通信息
func Info(format string, a ...interface{}) {
	infoColor.Printf("ℹ "+format+"\n", a...)
}

// Success 打印成功信息
func Success(format string, a ...interface{}) {
	successColor.Printf("✓ "+format+"\n", a...)
}

// Header 打印标题
func Header(format string, a ...interface{}) {
	headerColor.Printf("═══ "+format+" ═══\n", a...)
}

// Separator 打印分隔线
func Separator() {
	color.New(color.FgHiBlack).Println("────────────────────────────────────────────────────────")
}
