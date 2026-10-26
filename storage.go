package gws

import (
	"sync"
	"time"
)

type storage struct {
	mu   sync.RWMutex
	Keys map[string]any
}

// Set 保存当前连接独占的键值，首次写入时初始化表。
func (c *storage) Set(key string, value any) {
	c.mu.Lock()
	if c.Keys == nil {
		c.Keys = make(map[string]any)
	}
	c.Keys[key] = value
	c.mu.Unlock()
}

// Get 返回 key 对应的值。不存在时 exists 为 false。
func (c *storage) Get(key string) (value any, exists bool) {
	c.mu.RLock()
	value, exists = c.Keys[key]
	c.mu.RUnlock()
	return
}

// GetString 以 string 取出值，类型不匹配时返回空字符串。
func (c *storage) GetString(key string) (s string) {
	if val, ok := c.Get(key); ok && val != nil {
		s, _ = val.(string)
	}
	return
}

// GetBool 以 bool 取出值，类型不匹配时返回 false。
func (c *storage) GetBool(key string) (b bool) {
	if val, ok := c.Get(key); ok && val != nil {
		b, _ = val.(bool)
	}
	return
}

// GetInt 以 int 取出值，类型不匹配时返回 0。
func (c *storage) GetInt(key string) (i int) {
	if val, ok := c.Get(key); ok && val != nil {
		i, _ = val.(int)
	}
	return
}

// GetInt64 以 int64 取出值，类型不匹配时返回 0。
func (c *storage) GetInt64(key string) (i64 int64) {
	if val, ok := c.Get(key); ok && val != nil {
		i64, _ = val.(int64)
	}
	return
}

// GetUint 以 uint 取出值，类型不匹配时返回 0。
func (c *storage) GetUint(key string) (ui uint) {
	if val, ok := c.Get(key); ok && val != nil {
		ui, _ = val.(uint)
	}
	return
}

// GetUint64 以 uint64 取出值，类型不匹配时返回 0。
func (c *storage) GetUint64(key string) (ui64 uint64) {
	if val, ok := c.Get(key); ok && val != nil {
		ui64, _ = val.(uint64)
	}
	return
}

// GetFloat64 以 float64 取出值，类型不匹配时返回 0。
func (c *storage) GetFloat64(key string) (f64 float64) {
	if val, ok := c.Get(key); ok && val != nil {
		f64, _ = val.(float64)
	}
	return
}

// GetTime 以 time.Time 取出值，类型不匹配时返回零值。
func (c *storage) GetTime(key string) (t time.Time) {
	if val, ok := c.Get(key); ok && val != nil {
		t, _ = val.(time.Time)
	}
	return
}

// GetDuration 以 time.Duration 取出值，类型不匹配时返回 0。
func (c *storage) GetDuration(key string) (d time.Duration) {
	if val, ok := c.Get(key); ok && val != nil {
		d, _ = val.(time.Duration)
	}
	return
}

// GetStringSlice 以 []string 取出值，类型不匹配时返回 nil。
func (c *storage) GetStringSlice(key string) (ss []string) {
	if val, ok := c.Get(key); ok && val != nil {
		ss, _ = val.([]string)
	}
	return
}

// GetStringMap 以 map[string]any 取出值，类型不匹配时返回 nil。
func (c *storage) GetStringMap(key string) (sm map[string]any) {
	if val, ok := c.Get(key); ok && val != nil {
		sm, _ = val.(map[string]any)
	}
	return
}

// GetStringMapString 以 map[string]string 取出值，类型不匹配时返回 nil。
func (c *storage) GetStringMapString(key string) (sms map[string]string) {
	if val, ok := c.Get(key); ok && val != nil {
		sms, _ = val.(map[string]string)
	}
	return
}

// GetStringMapStringSlice 以 map[string][]string 取出值，类型不匹配时返回 nil。
func (c *storage) GetStringMapStringSlice(key string) (smss map[string][]string) {
	if val, ok := c.Get(key); ok && val != nil {
		smss, _ = val.(map[string][]string)
	}
	return
}
