package jsonquery

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// Query JSON日志查询器
type Query struct {
	SelectFields []string     // 选择的字段路径
	Conditions   []Condition  // 过滤条件
	Format       OutputFormat // 输出格式
}

// Condition 查询条件
type Condition struct {
	Field    string // JSON字段路径 (如 "request.method")
	Operator string // 操作符: eq, ne, gt, lt, gte, lte, contains, regex
	Value    string // 比较值
}

// OutputFormat 输出格式
type OutputFormat int

const (
	FormatRaw    OutputFormat = iota // 原始JSON
	FormatFlat                       // 扁平化KV
	FormatTable                      // 表格形式
	FormatFields                     // 仅选择的字段
)

// NewQuery 创建查询
func NewQuery() *Query {
	return &Query{
		Format: FormatRaw,
	}
}

// Select 选择字段
func (q *Query) Select(fields ...string) *Query {
	q.SelectFields = append(q.SelectFields, fields...)
	return q
}

// Where 添加条件
func (q *Query) Where(field, operator, value string) *Query {
	q.Conditions = append(q.Conditions, Condition{
		Field:    field,
		Operator: operator,
		Value:    value,
	})
	return q
}

// SetFormat 设置输出格式
func (q *Query) SetFormat(format OutputFormat) *Query {
	q.Format = format
	return q
}

// Match 判断一行JSON是否匹配所有条件
func (q *Query) Match(jsonLine string) bool {
	if !gjson.Valid(jsonLine) {
		return false
	}

	for _, cond := range q.Conditions {
		result := gjson.Get(jsonLine, cond.Field)
		if !result.Exists() {
			return false
		}

		if !matchCondition(result, cond) {
			return false
		}
	}
	return true
}

// Extract 提取选中字段
func (q *Query) Extract(jsonLine string) map[string]interface{} {
	if !gjson.Valid(jsonLine) {
		return nil
	}

	fields := make(map[string]interface{})
	for _, field := range q.SelectFields {
		result := gjson.Get(jsonLine, field)
		if result.Exists() {
			fields[field] = result.Value()
		}
	}
	return fields
}

// FormatLine 格式化输出
func (q *Query) FormatLine(jsonLine string) string {
	if !gjson.Valid(jsonLine) {
		return jsonLine // 非JSON直接返回
	}

	switch q.Format {
	case FormatFields:
		return q.formatSelectedFields(jsonLine)
	case FormatFlat:
		return q.formatFlat(jsonLine)
	case FormatTable:
		return q.formatTable(jsonLine)
	default:
		return jsonLine
	}
}

func (q *Query) formatSelectedFields(jsonLine string) string {
	if len(q.SelectFields) == 0 {
		return jsonLine
	}

	var parts []string
	for _, field := range q.SelectFields {
		result := gjson.Get(jsonLine, field)
		if result.Exists() {
			parts = append(parts, fmt.Sprintf("%s=%v", field, result.Value()))
		}
	}
	return strings.Join(parts, " | ")
}

func (q *Query) formatFlat(jsonLine string) string {
	var parts []string
	result := gjson.Parse(jsonLine)
	result.ForEach(func(key, value gjson.Result) bool {
		parts = append(parts, fmt.Sprintf("%s=%v", key.String(), value.Value()))
		return true
	})
	return strings.Join(parts, " ")
}

func (q *Query) formatTable(jsonLine string) string {
	if len(q.SelectFields) == 0 {
		return jsonLine
	}

	var parts []string
	for _, field := range q.SelectFields {
		result := gjson.Get(jsonLine, field)
		val := ""
		if result.Exists() {
			val = result.String()
		}
		parts = append(parts, fmt.Sprintf("%-20s", val))
	}
	return strings.Join(parts, " | ")
}

// matchCondition 匹配条件
func matchCondition(result gjson.Result, cond Condition) bool {
	switch cond.Operator {
	case "eq", "==", "=":
		return result.String() == cond.Value
	case "ne", "!=":
		return result.String() != cond.Value
	case "gt", ">":
		return result.Float() > parseFloat(cond.Value)
	case "lt", "<":
		return result.Float() < parseFloat(cond.Value)
	case "gte", ">=":
		return result.Float() >= parseFloat(cond.Value)
	case "lte", "<=":
		return result.Float() <= parseFloat(cond.Value)
	case "contains":
		return strings.Contains(result.String(), cond.Value)
	case "prefix":
		return strings.HasPrefix(result.String(), cond.Value)
	case "suffix":
		return strings.HasSuffix(result.String(), cond.Value)
	default:
		return true
	}
}

func parseFloat(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

// IsJSON 检查字符串是否为有效JSON
func IsJSON(s string) bool {
	return gjson.Valid(s)
}

// GetField 快速获取JSON字段值
func GetField(jsonLine, field string) string {
	result := gjson.Get(jsonLine, field)
	if !result.Exists() {
		return ""
	}
	return result.String()
}

// GetNestedField 获取嵌套字段
func GetNestedField(jsonLine string, path ...string) string {
	return GetField(jsonLine, strings.Join(path, "."))
}

// ParseConditionString 解析条件字符串 (field=value, field>value 等)
func ParseConditionString(s string) (Condition, error) {
	operators := []string{">=", "<=", "!=", "==", ">", "<", "="}
	for _, op := range operators {
		idx := strings.Index(s, op)
		if idx > 0 {
			return Condition{
				Field:    strings.TrimSpace(s[:idx]),
				Operator: op,
				Value:    strings.TrimSpace(s[idx+len(op):]),
			}, nil
		}
	}
	return Condition{}, fmt.Errorf("无法解析条件: %s", s)
}
