// Package cache provides a bounded LRU of immutable byte snapshots.
package cache

import (
	"bytes"
	"container/list"
	"sync"
	"time"

	"github.com/cpanato/berlin-transit/internal/provider"
)

type Entry struct {
	Value       []byte
	StoredAt    time.Time
	FreshUntil  time.Time
	RetainUntil time.Time
	Policy      provider.Policy
}

type item struct {
	key   string
	entry Entry
}

func clone(e Entry) Entry {
	e.Value = bytes.Clone(e.Value)
	if e.Policy.MaxAge != nil {
		d := *e.Policy.MaxAge
		e.Policy.MaxAge = &d
	}
	return e
}

type Cache struct {
	mu                         sync.Mutex
	entries                    map[string]*list.Element
	lru                        *list.List
	maxEntries, maxBytes, size int
	now                        func() time.Time
}

func New(maxEntries, maxBytes int, now func() time.Time) *Cache {
	if maxEntries < 1 || maxBytes < 1 || now == nil {
		panic("invalid cache configuration")
	}
	return &Cache{entries: make(map[string]*list.Element), lru: list.New(), maxEntries: maxEntries, maxBytes: maxBytes, now: now}
}

func (c *Cache) remove(e *list.Element) {
	v := e.Value.(item)
	delete(c.entries, v.key)
	c.size -= len(v.key) + len(v.entry.Value)
	c.lru.Remove(e)
}

func (c *Cache) Get(key string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return Entry{}, false
	}
	v := e.Value.(item)
	if !c.now().Before(v.entry.RetainUntil) {
		c.remove(e)
		return Entry{}, false
	}
	c.lru.MoveToFront(e)
	v.entry = clone(v.entry)
	return v.entry, true
}

func (c *Cache) Set(key string, entry Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		c.remove(e)
	}
	weight := len(key) + len(entry.Value)
	if weight > c.maxBytes || !c.now().Before(entry.RetainUntil) {
		return
	}
	// Remove expired entries before evicting usable snapshots.
	for e := c.lru.Back(); e != nil; {
		previous := e.Prev()
		if !c.now().Before(e.Value.(item).entry.RetainUntil) {
			c.remove(e)
		}
		e = previous
	}
	for len(c.entries) >= c.maxEntries || c.size+weight > c.maxBytes {
		c.remove(c.lru.Back())
	}
	entry = clone(entry)
	c.entries[key] = c.lru.PushFront(item{key: key, entry: entry})
	c.size += weight
}

func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		c.remove(e)
	}
}

func (c *Cache) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.entries) }
