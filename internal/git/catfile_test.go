package git

import (
	"strconv"
	"testing"
)

func TestBlobCacheBasicHitMiss(t *testing.T) {
	c := newBlobCache(10)
	const h = "abc123"

	if _, ok := c.get(h); ok {
		t.Fatal("empty cache should miss")
	}

	c.put(h, 42)

	size, ok := c.get(h)
	if !ok {
		t.Fatal("cache should hit after put")
	}
	if size != 42 {
		t.Errorf("size = %d, want 42", size)
	}
}

func TestBlobCacheLRUEviction(t *testing.T) {
	c := newBlobCache(3)
	c.put("a", 1)
	c.put("b", 2)
	c.put("c", 3)
	// "a" is the oldest entry; adding a fourth must evict it.
	c.put("d", 4)

	if _, ok := c.get("a"); ok {
		t.Error("'a' should be evicted after overflow")
	}
	if size, ok := c.get("b"); !ok || size != 2 {
		t.Errorf("'b' should still be present, got size=%d ok=%v", size, ok)
	}
	if size, ok := c.get("d"); !ok || size != 4 {
		t.Errorf("'d' should be present, got size=%d ok=%v", size, ok)
	}
}

func TestBlobCacheAccessUpdatesRecency(t *testing.T) {
	// get() must refresh LRU order so heavily-accessed entries survive
	// eviction. Without this, a file persisting across many commits
	// would be evicted between queries, defeating the purpose.
	c := newBlobCache(3)
	c.put("a", 1)
	c.put("b", 2)
	c.put("c", 3)

	if _, ok := c.get("a"); !ok {
		t.Fatal("'a' should be present")
	}
	// "b" is now the oldest entry.
	c.put("d", 4)

	if _, ok := c.get("a"); !ok {
		t.Error("'a' should still be present after recency update")
	}
	if _, ok := c.get("b"); ok {
		t.Error("'b' should be evicted (was least recently used)")
	}
}

func TestBlobCacheUpdateExistingKey(t *testing.T) {
	// Re-putting an existing hash must update the size and refresh
	// recency without growing the list or evicting siblings.
	c := newBlobCache(3)
	c.put("a", 1)
	c.put("a", 99)

	size, ok := c.get("a")
	if !ok || size != 99 {
		t.Errorf("update existing key: got size=%d ok=%v, want 99 true", size, ok)
	}
	if c.ll.Len() != 1 {
		t.Errorf("update existing should not grow: len=%d", c.ll.Len())
	}
}

func TestBlobCacheZeroCap(t *testing.T) {
	// A cap-0 cache acts as a no-op — all puts are dropped, all gets
	// miss. Exercised mostly to guard against accidental divide-by-zero
	// or eviction-loop bugs if someone ever passes 0.
	c := newBlobCache(0)
	c.put("a", 1)
	if _, ok := c.get("a"); ok {
		t.Error("zero-cap cache should always miss")
	}
}

func TestBlobCacheNilSafe(t *testing.T) {
	// Defensive: nil cache shouldn't panic on get/put. Makes the
	// resolver robust if cache ever ends up nil during shutdown.
	var c *blobCache
	c.put("a", 1)
	if _, ok := c.get("a"); ok {
		t.Error("nil cache should always miss")
	}
}

func TestBlobCacheFillAndOverflow(t *testing.T) {
	// Beat up the cache with 2x capacity in puts and confirm exactly
	// the oldest half was evicted and the newer half is intact.
	const cap = 100
	c := newBlobCache(cap)
	for i := 0; i < 2*cap; i++ {
		c.put(fmtHash(i), int64(i))
	}
	for i := 0; i < cap; i++ {
		if _, ok := c.get(fmtHash(i)); ok {
			t.Errorf("entry %d should have been evicted", i)
		}
	}
	for i := cap; i < 2*cap; i++ {
		size, ok := c.get(fmtHash(i))
		if !ok || size != int64(i) {
			t.Errorf("entry %d: size=%d ok=%v, want size=%d present", i, size, ok, i)
		}
	}
}

func fmtHash(i int) string {
	return "hash-" + strconv.Itoa(i)
}
