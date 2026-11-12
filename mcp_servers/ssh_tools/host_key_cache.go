package main

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/executiontarget"
)

type hostKeyCacheContextKey struct{}

type cachedHostKey struct {
	line    string
	expires time.Time
}

type hostKeyCache struct {
	mu      sync.Mutex
	entries map[string]cachedHostKey
}

func withHostKeyCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, hostKeyCacheContextKey{}, &hostKeyCache{entries: make(map[string]cachedHostKey)})
}

func cachedPinnedHostKey(ctx context.Context, target executiontarget.Target, line string) string {
	cache, _ := ctx.Value(hostKeyCacheContextKey{}).(*hostKeyCache)
	if cache == nil {
		return ""
	}
	key := target.Host + ":" + strconv.Itoa(target.Port) + ":" + target.HostKeySHA256
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if line != "" {
		if len(cache.entries) >= 64 {
			for old := range cache.entries {
				delete(cache.entries, old)
				break
			}
		}
		cache.entries[key] = cachedHostKey{line: line, expires: time.Now().Add(2 * time.Minute)}
		return line
	}
	entry := cache.entries[key]
	if time.Now().Before(entry.expires) {
		return entry.line
	}
	delete(cache.entries, key)
	return ""
}
