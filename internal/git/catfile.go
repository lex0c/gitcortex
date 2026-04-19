package git

import (
	"bufio"
	"container/list"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

const (
	nullHash = "0000000000000000000000000000000000000000"

	// blobCacheMaxEntries is the LRU size for BlobSizeResolver's across-
	// commit cache. Git content-addresses blobs by hash, so size for a
	// given hash is permanent — caching is semantically safe. A single
	// file persists across many consecutive commits in real history;
	// without a cache, every commit queries cat-file for hashes we
	// already resolved moments earlier. 50k entries ≈ 7MB RAM, enough
	// to hold the "hot" working set on large repos like Chromium.
	blobCacheMaxEntries = 50_000
)

// blobCache is a simple LRU of hash → blob size. Not thread-safe; the
// resolver is called from a single extract goroutine.
type blobCache struct {
	capacity int
	m        map[string]*list.Element
	ll       *list.List
}

type blobCacheEntry struct {
	hash string
	size int64
}

func newBlobCache(capacity int) *blobCache {
	return &blobCache{
		capacity: capacity,
		m:        make(map[string]*list.Element, capacity),
		ll:       list.New(),
	}
}

func (c *blobCache) get(hash string) (int64, bool) {
	if c == nil || c.capacity == 0 {
		return 0, false
	}
	if e, ok := c.m[hash]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*blobCacheEntry).size, true
	}
	return 0, false
}

func (c *blobCache) put(hash string, size int64) {
	if c == nil || c.capacity == 0 {
		return
	}
	if e, ok := c.m[hash]; ok {
		c.ll.MoveToFront(e)
		e.Value.(*blobCacheEntry).size = size
		return
	}
	e := c.ll.PushFront(&blobCacheEntry{hash: hash, size: size})
	c.m[hash] = e
	if c.ll.Len() > c.capacity {
		oldest := c.ll.Back()
		c.ll.Remove(oldest)
		delete(c.m, oldest.Value.(*blobCacheEntry).hash)
	}
}

type BlobSizeResolver struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Scanner
	cancel context.CancelFunc
	cache  *blobCache
}

func NewBlobSizeResolver(ctx context.Context, repo string) (*BlobSizeResolver, error) {
	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "git", "-C", repo, "cat-file", "--batch-check")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cat-file stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cat-file stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("cat-file start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)

	return &BlobSizeResolver{
		cmd:    cmd,
		stdin:  stdin,
		reader: scanner,
		cancel: cancel,
		cache:  newBlobCache(blobCacheMaxEntries),
	}, nil
}

func (r *BlobSizeResolver) Resolve(entries []RawEntry) (map[string]int64, error) {
	sizes := make(map[string]int64, len(entries)*2)
	needed := make(map[string]struct{})
	for _, e := range entries {
		if e.OldHash != nullHash && e.OldHash != "" {
			if size, ok := r.cache.get(e.OldHash); ok {
				sizes[e.OldHash] = size
			} else {
				needed[e.OldHash] = struct{}{}
			}
		}
		if e.NewHash != nullHash && e.NewHash != "" {
			if size, ok := r.cache.get(e.NewHash); ok {
				sizes[e.NewHash] = size
			} else {
				needed[e.NewHash] = struct{}{}
			}
		}
	}

	if len(needed) == 0 {
		return sizes, nil
	}

	// Write hashes in a goroutine to avoid deadlock: if we write all hashes
	// before reading, the OS pipe buffer (64KB) can fill up. cat-file blocks
	// trying to write responses, and we block trying to write more hashes.
	// The channel has buffer 1 so the goroutine always completes (no leak).
	writeErr := make(chan error, 1)
	go func() {
		for h := range needed {
			if _, err := io.WriteString(r.stdin, h+"\n"); err != nil {
				writeErr <- fmt.Errorf("cat-file write: %w", err)
				return
			}
		}
		writeErr <- nil
	}()

	for i := 0; i < len(needed); i++ {
		if !r.reader.Scan() {
			if err := r.reader.Err(); err != nil {
				return nil, fmt.Errorf("cat-file read: %w", err)
			}
			return nil, fmt.Errorf("cat-file: unexpected EOF after %d/%d responses", i, len(needed))
		}

		parts := strings.Fields(r.reader.Text())
		if len(parts) < 3 || parts[1] != "blob" {
			continue
		}

		size, err := parseInt64(parts[2])
		if err != nil {
			continue
		}
		sizes[parts[0]] = size
		r.cache.put(parts[0], size)
	}

	if err := <-writeErr; err != nil {
		return nil, err
	}

	return sizes, nil
}

func (r *BlobSizeResolver) Close() error {
	r.stdin.Close()
	r.cancel()
	_ = r.cmd.Wait()
	return nil
}
