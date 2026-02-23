package pipeline

import (
	"regexp"
	"strings"
	"time"
)

// LogLine 日志行
type LogLine struct {
	Raw           string
	Namespace     string
	PodName       string
	ContainerName string
	Timestamp     time.Time
	Fields        map[string]string // 解析后的字段
	Filtered      bool              // 是否被过滤
}

// Stage 处理阶段接口
type Stage interface {
	Process(line *LogLine) *LogLine
	Name() string
}

// Pipeline 日志处理管道
type Pipeline struct {
	stages []Stage
}

// NewPipeline 创建管道
func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// AddStage 添加处理阶段
func (p *Pipeline) AddStage(stage Stage) *Pipeline {
	p.stages = append(p.stages, stage)
	return p
}

// Process 处理一行日志
func (p *Pipeline) Process(line *LogLine) *LogLine {
	for _, stage := range p.stages {
		if line == nil || line.Filtered {
			return line
		}
		line = stage.Process(line)
	}
	return line
}

// ProcessBatch 批量处理
func (p *Pipeline) ProcessBatch(lines []*LogLine) []*LogLine {
	var results []*LogLine
	for _, line := range lines {
		result := p.Process(line)
		if result != nil && !result.Filtered {
			results = append(results, result)
		}
	}
	return results
}

// --- 内置处理阶段 ---

// GrepStage 关键词过滤
type GrepStage struct {
	pattern    string
	invert     bool
	ignoreCase bool
}

func NewGrepStage(pattern string, invert, ignoreCase bool) *GrepStage {
	return &GrepStage{pattern: pattern, invert: invert, ignoreCase: ignoreCase}
}

func (s *GrepStage) Name() string { return "grep" }

func (s *GrepStage) Process(line *LogLine) *LogLine {
	content := line.Raw
	pattern := s.pattern

	if s.ignoreCase {
		content = strings.ToLower(content)
		pattern = strings.ToLower(pattern)
	}

	match := strings.Contains(content, pattern)
	if s.invert {
		match = !match
	}
	if !match {
		line.Filtered = true
	}
	return line
}

// RegexStage 正则过滤
type RegexStage struct {
	re     *regexp.Regexp
	invert bool
}

func NewRegexStage(pattern string, invert bool) (*RegexStage, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return &RegexStage{re: re, invert: invert}, nil
}

func (s *RegexStage) Name() string { return "regex" }

func (s *RegexStage) Process(line *LogLine) *LogLine {
	match := s.re.MatchString(line.Raw)
	if s.invert {
		match = !match
	}
	if !match {
		line.Filtered = true
	}
	return line
}

// LevelFilterStage 日志级别过滤
type LevelFilterStage struct {
	levels map[string]bool
}

func NewLevelFilterStage(levels []string) *LevelFilterStage {
	m := make(map[string]bool)
	for _, l := range levels {
		m[strings.ToUpper(l)] = true
	}
	return &LevelFilterStage{levels: m}
}

func (s *LevelFilterStage) Name() string { return "level_filter" }

func (s *LevelFilterStage) Process(line *LogLine) *LogLine {
	upper := strings.ToUpper(line.Raw)
	found := false
	for level := range s.levels {
		if strings.Contains(upper, level) {
			found = true
			break
		}
	}
	if !found {
		line.Filtered = true
	}
	return line
}

// TimeRangeStage 时间范围过滤
type TimeRangeStage struct {
	start time.Time
	end   time.Time
}

func NewTimeRangeStage(start, end time.Time) *TimeRangeStage {
	return &TimeRangeStage{start: start, end: end}
}

func (s *TimeRangeStage) Name() string { return "time_range" }

func (s *TimeRangeStage) Process(line *LogLine) *LogLine {
	if line.Timestamp.IsZero() {
		return line // 无时间戳则通过
	}
	if line.Timestamp.Before(s.start) || line.Timestamp.After(s.end) {
		line.Filtered = true
	}
	return line
}

// DeduplicateStage 去重阶段
type DeduplicateStage struct {
	seen map[string]bool
}

func NewDeduplicateStage() *DeduplicateStage {
	return &DeduplicateStage{seen: make(map[string]bool)}
}

func (s *DeduplicateStage) Name() string { return "deduplicate" }

func (s *DeduplicateStage) Process(line *LogLine) *LogLine {
	if s.seen[line.Raw] {
		line.Filtered = true
		return line
	}
	s.seen[line.Raw] = true
	return line
}

// TransformStage 自定义转换
type TransformStage struct {
	transformFn func(string) string
}

func NewTransformStage(fn func(string) string) *TransformStage {
	return &TransformStage{transformFn: fn}
}

func (s *TransformStage) Name() string { return "transform" }

func (s *TransformStage) Process(line *LogLine) *LogLine {
	line.Raw = s.transformFn(line.Raw)
	return line
}

// FieldExtractStage 字段提取（正则命名捕获组）
type FieldExtractStage struct {
	re *regexp.Regexp
}

func NewFieldExtractStage(pattern string) (*FieldExtractStage, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return &FieldExtractStage{re: re}, nil
}

func (s *FieldExtractStage) Name() string { return "field_extract" }

func (s *FieldExtractStage) Process(line *LogLine) *LogLine {
	if line.Fields == nil {
		line.Fields = make(map[string]string)
	}
	matches := s.re.FindStringSubmatch(line.Raw)
	if matches == nil {
		return line
	}
	for i, name := range s.re.SubexpNames() {
		if i != 0 && name != "" && i < len(matches) {
			line.Fields[name] = matches[i]
		}
	}
	return line
}

// TailStage 尾部N行
type TailStage struct {
	n      int
	buffer []*LogLine
}

func NewTailStage(n int) *TailStage {
	return &TailStage{n: n}
}

func (s *TailStage) Name() string { return "tail" }

func (s *TailStage) Process(line *LogLine) *LogLine {
	s.buffer = append(s.buffer, line)
	if len(s.buffer) > s.n {
		s.buffer = s.buffer[1:]
	}
	// 在流式处理中全部放行，由 Flush 决定最终输出
	return line
}

func (s *TailStage) Flush() []*LogLine {
	return s.buffer
}

// CountStage 计数阶段
type CountStage struct {
	Count int
}

func NewCountStage() *CountStage {
	return &CountStage{}
}

func (s *CountStage) Name() string { return "count" }

func (s *CountStage) Process(line *LogLine) *LogLine {
	s.Count++
	return line
}
