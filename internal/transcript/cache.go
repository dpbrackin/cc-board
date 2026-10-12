package transcript

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

type cacheKey struct {
	size  int64
	mtime time.Time
}

type cacheEntry struct {
	key  cacheKey
	info Info
}

// Cache holds parsed transcripts keyed by path, re-parsing a file only when
// its size or mtime changes. It is safe for concurrent use.
type Cache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	parses  int
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{entries: map[string]cacheEntry{}} }

// Parses returns how many files have been parsed (for tests).
func (c *Cache) Parses() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.parses
}

// Get returns the parsed transcript at path, using the cache when possible.
func (c *Cache) Get(path string) (Info, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Info{}, err
	}
	key := cacheKey{size: fi.Size(), mtime: fi.ModTime()}
	c.mu.Lock()
	if e, ok := c.entries[path]; ok && e.key == key {
		c.mu.Unlock()
		return e.info, nil
	}
	c.mu.Unlock()

	info, err := ParseFile(path)
	if err != nil {
		return info, err
	}
	c.mu.Lock()
	c.entries[path] = cacheEntry{key: key, info: info}
	c.parses++
	c.mu.Unlock()
	return info, nil
}

// Scan parses every top-level transcript under projectsDir
// (projectsDir/<encoded-path>/<session_id>.jsonl). Subagent transcripts in
// deeper directories are not sessions and are skipped. Files that vanished
// are evicted from the cache.
func (c *Cache) Scan(projectsDir string) ([]Info, error) {
	paths, err := filepath.Glob(filepath.Join(projectsDir, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(paths))
	out := make([]Info, 0, len(paths))
	for _, p := range paths {
		info, err := c.Get(p)
		if err != nil {
			continue
		}
		seen[p] = true
		out = append(out, info)
	}
	c.mu.Lock()
	for p := range c.entries {
		if !seen[p] {
			delete(c.entries, p)
		}
	}
	c.mu.Unlock()
	return out, nil
}
