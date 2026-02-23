package engine

import (
	"sync"
)

// Event 内部系统事件
type Event struct {
	Type string
	Data interface{}
}

// EventBus 引擎内置消息总线 (解耦组件)
type EventBus struct {
	subs map[string][]chan Event
	mu   sync.RWMutex
}

func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[string][]chan Event),
	}
}

func (eb *EventBus) Subscribe(topic string) chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan Event, 100)
	eb.subs[topic] = append(eb.subs[topic], ch)
	return ch
}

func (eb *EventBus) Publish(topic string, ev Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.subs[topic] {
		select {
		case ch <- ev:
		default: // 溢出抛弃，保证总线不阻塞
		}
	}
}
