package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/provider"
)

func TestExpiryEvictionAndIsolation(t *testing.T) {
	now := time.Unix(100, 0)
	c := New(2, 20, func() time.Time { return now })
	age := time.Second
	e := Entry{Value: []byte("one"), StoredAt: now, FreshUntil: now.Add(time.Second), RetainUntil: now.Add(2 * time.Second), Policy: provider.Policy{MaxAge: &age}}
	c.Set("a", e)
	e.Value[0] = 'X'
	age = time.Hour
	got, ok := c.Get("a")
	if !ok || string(got.Value) != "one" || *got.Policy.MaxAge != time.Second {
		t.Fatal("cache shares input memory")
	}
	got.Value[0] = 'Y'
	*got.Policy.MaxAge = time.Hour
	c.Set("b", e)
	_, _ = c.Get("a")
	c.Set("c", e)
	if _, ok := c.Get("b"); ok {
		t.Error("LRU entry retained")
	}
	got, _ = c.Get("a")
	if string(got.Value) != "one" || *got.Policy.MaxAge != time.Second {
		t.Error("cache shares output memory")
	}
	c.Set("a", Entry{Value: []byte("changed"), RetainUntil: now.Add(time.Second)})
	if c.Len() != 2 {
		t.Error("replacement changed count")
	}
	c.Set("large", Entry{Value: make([]byte, 30), RetainUntil: now.Add(time.Hour)})
	if c.Len() != 2 {
		t.Error("oversized entry evicted useful data")
	}
	now = now.Add(time.Second)
	if _, ok := c.Get("a"); ok {
		t.Error("expired entry returned")
	}
	now = now.Add(time.Second)
	c.Set("d", Entry{Value: []byte("ok"), RetainUntil: now.Add(time.Second)})
	if c.Len() != 1 {
		t.Error("expired entries not purged")
	}
	c.Delete("d")
	c.Delete("missing")
	if c.Len() != 0 {
		t.Fatal("delete failed")
	}
	c.Set("expired", Entry{RetainUntil: now})
	if c.Len() != 0 {
		t.Error("stored expired entry")
	}
	c.Set("a", Entry{Value: make([]byte, 15), RetainUntil: now.Add(time.Second)})
	c.Set("b", Entry{Value: make([]byte, 15), RetainUntil: now.Add(time.Second)})
	if c.Len() != 1 {
		t.Error("byte budget not enforced")
	}
}

func TestConcurrentCache(t *testing.T) {
	c := New(10, 1000, time.Now)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			for j := range 50 {
				k := fmt.Sprint(i % 10)
				c.Set(k, Entry{Value: []byte("test"), RetainUntil: time.Now().Add(time.Minute)})
				_, _ = c.Get(k)
				if j%3 == 0 {
					c.Delete(k)
				}
				_ = c.Len()
			}
		})
	}
	wg.Wait()
	if c.Len() > 10 {
		t.Fatal("entry budget exceeded")
	}
}

func TestInvalidCacheConfiguration(t *testing.T) {
	for _, limits := range [][2]int{{0, 1}, {1, 0}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("accepted invalid cache")
				}
			}()
			_ = New(limits[0], limits[1], time.Now)
		}()
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("accepted missing clock")
			}
		}()
		_ = New(1, 1, nil)
	}()
}
