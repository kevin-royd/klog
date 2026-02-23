package cache

import (
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogEntry 日志索引条目
type LogEntry struct {
	Namespace     string    `json:"namespace"`
	PodName       string    `json:"pod_name"`
	ContainerName string    `json:"container_name"`
	Timestamp     time.Time `json:"timestamp"`
	Line          string    `json:"line"`
	LineNumber    int       `json:"line_number"`
	Level         string    `json:"level"`    // INFO, WARN, ERROR, DEBUG
	TraceID       string    `json:"trace_id"` // 提取的trace id
}

// CacheItem 缓存项
type CacheItem struct {
	Key       string      `json:"key"`
	Entries   []*LogEntry `json:"entries"`
	CreatedAt time.Time   `json:"created_at"`
	TTL       time.Duration
}

// IsExpired 检查是否过期
func (c *CacheItem) IsExpired() bool {
	if c.TTL == 0 {
		return false
	}
	return time.Since(c.CreatedAt) > c.TTL
}

// LogCache LRU 日志索引缓存
type LogCache struct {
	mu       sync.RWMutex
	capacity int
	ttl      time.Duration
	items    map[string]*list.Element
	lru      *list.List
	cacheDir string
}

// NewLogCache 创建日志缓存
func NewLogCache(capacity int, ttl time.Duration) *LogCache {
	homeDir, _ := os.UserHomeDir()
	cacheDir := filepath.Join(homeDir, ".klog", "cache")
	os.MkdirAll(cacheDir, 0755)

	c := &LogCache{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[string]*list.Element),
		lru:      list.New(),
		cacheDir: cacheDir,
	}

	// 异步清理过期缓存
	go c.cleanupLoop()

	return c
}

// GenerateKey 生成缓存key
func GenerateKey(namespace, podName, container string, since time.Duration) string {
	raw := fmt.Sprintf("%s/%s/%s/%v", namespace, podName, container, since)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:8])
}

// Put 写入缓存
func (c *LogCache) Put(key string, entries []*LogEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 如果已存在，移到头部并更新
	if elem, ok := c.items[key]; ok {
		c.lru.MoveToFront(elem)
		item := elem.Value.(*CacheItem)
		item.Entries = entries
		item.CreatedAt = time.Now()
		return
	}

	// 容量检查，驱逐最久未使用
	for c.lru.Len() >= c.capacity {
		c.evict()
	}

	item := &CacheItem{
		Key:       key,
		Entries:   entries,
		CreatedAt: time.Now(),
		TTL:       c.ttl,
	}

	elem := c.lru.PushFront(item)
	c.items[key] = elem
}

// Get 读取缓存
func (c *LogCache) Get(key string) ([]*LogEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	item := elem.Value.(*CacheItem)
	if item.IsExpired() {
		return nil, false
	}

	c.lru.MoveToFront(elem)
	return item.Entries, true
}

// Search 搜索缓存中的日志
func (c *LogCache) Search(keyword string) []*LogEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var results []*LogEntry
	for elem := c.lru.Front(); elem != nil; elem = elem.Next() {
		item := elem.Value.(*CacheItem)
		if item.IsExpired() {
			continue
		}
		for _, entry := range item.Entries {
			if strings.Contains(strings.ToLower(entry.Line), strings.ToLower(keyword)) {
				results = append(results, entry)
			}
		}
	}
	return results
}

// SearchByLevel 按日志级别搜索
func (c *LogCache) SearchByLevel(level string) []*LogEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var results []*LogEntry
	level = strings.ToUpper(level)
	for elem := c.lru.Front(); elem != nil; elem = elem.Next() {
		item := elem.Value.(*CacheItem)
		if item.IsExpired() {
			continue
		}
		for _, entry := range item.Entries {
			if strings.EqualFold(entry.Level, level) {
				results = append(results, entry)
			}
		}
	}
	return results
}

// SearchByTraceID 按TraceID搜索
func (c *LogCache) SearchByTraceID(traceID string) []*LogEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var results []*LogEntry
	for elem := c.lru.Front(); elem != nil; elem = elem.Next() {
		item := elem.Value.(*CacheItem)
		if item.IsExpired() {
			continue
		}
		for _, entry := range item.Entries {
			if entry.TraceID == traceID {
				results = append(results, entry)
			}
		}
	}
	return results
}

// SaveToDisk 持久化到磁盘
func (c *LogCache) SaveToDisk() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for key, elem := range c.items {
		item := elem.Value.(*CacheItem)
		if item.IsExpired() {
			continue
		}

		data, err := json.Marshal(item)
		if err != nil {
			continue
		}

		filePath := filepath.Join(c.cacheDir, key+".json")
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Errorf("写入缓存文件失败: %w", err)
		}
	}
	return nil
}

// LoadFromDisk 从磁盘加载缓存
func (c *LogCache) LoadFromDisk() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	entries, err := os.ReadDir(c.cacheDir)
	if err != nil {
		return nil // 缓存目录不存在时忽略
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(c.cacheDir, entry.Name()))
		if err != nil {
			continue
		}

		var item CacheItem
		if err := json.Unmarshal(data, &item); err != nil {
			continue
		}

		if item.IsExpired() {
			os.Remove(filepath.Join(c.cacheDir, entry.Name()))
			continue
		}

		elem := c.lru.PushBack(&item)
		c.items[item.Key] = elem
	}

	return nil
}

// Stats 缓存统计
func (c *LogCache) Stats() (size int, capacity int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Len(), c.capacity
}

// Clear 清除所有缓存
func (c *LogCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.lru.Init()

	// 清除磁盘缓存
	os.RemoveAll(c.cacheDir)
	os.MkdirAll(c.cacheDir, 0755)
}

// evict 驱逐最久未使用的缓存
func (c *LogCache) evict() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}

	item := elem.Value.(*CacheItem)
	delete(c.items, item.Key)
	c.lru.Remove(elem)

	// 删除磁盘文件
	os.Remove(filepath.Join(c.cacheDir, item.Key+".json"))
}

// cleanupLoop 异步清理过期缓存
func (c *LogCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		var toRemove []*list.Element
		for elem := c.lru.Front(); elem != nil; elem = elem.Next() {
			item := elem.Value.(*CacheItem)
			if item.IsExpired() {
				toRemove = append(toRemove, elem)
			}
		}
		for _, elem := range toRemove {
			item := elem.Value.(*CacheItem)
			delete(c.items, item.Key)
			c.lru.Remove(elem)
			os.Remove(filepath.Join(c.cacheDir, item.Key+".json"))
		}
		c.mu.Unlock()
	}
}
