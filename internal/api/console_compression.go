package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// The console ships around 1.2 MB of embedded CSS and JavaScript. Serving that
// uncompressed dominates first paint on every fresh load, so each asset is
// compressed and fingerprinted once and then reused: embedded files cannot change
// while the process runs.

// consoleAssetMinGzipSize skips compression for payloads small enough that gzip
// framing and the extra CPU outweigh the transfer saving.
const consoleAssetMinGzipSize = 1024

type consoleAsset struct {
	contentType string
	raw         []byte
	// gzipped stays nil when the asset is too small or did not actually shrink.
	gzipped  []byte
	etag     string
	gzipETag string
}

var (
	consoleAssetMu    sync.Mutex
	consoleAssetCache = map[string]*consoleAsset{}
)

// consoleAssetByName reads an embedded console file and caches its compressed
// representation and entity tags.
func consoleAssetByName(name string) (*consoleAsset, bool) {
	consoleAssetMu.Lock()
	defer consoleAssetMu.Unlock()
	if cached, ok := consoleAssetCache[name]; ok {
		return cached, cached != nil
	}
	raw, err := consoleFS.ReadFile(name)
	if err != nil {
		// Cache the miss so a probing client cannot force repeated FS lookups.
		consoleAssetCache[name] = nil
		return nil, false
	}
	sum := sha256.Sum256(raw)
	fingerprint := base64.RawURLEncoding.EncodeToString(sum[:12])
	asset := &consoleAsset{
		contentType: consoleAssetContentType(name),
		raw:         raw,
		etag:        `"` + fingerprint + `"`,
		gzipETag:    `"` + fingerprint + `-gz"`,
	}
	if len(raw) >= consoleAssetMinGzipSize {
		var buffer bytes.Buffer
		writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
		if err == nil {
			if _, err = writer.Write(raw); err == nil && writer.Close() == nil && buffer.Len() < len(raw) {
				asset.gzipped = buffer.Bytes()
			}
		}
	}
	consoleAssetCache[name] = asset
	return asset, true
}

func consoleAssetContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json; charset=utf-8"
	}
	if guessed := mime.TypeByExtension(path.Ext(name)); guessed != "" {
		return guessed
	}
	return "application/octet-stream"
}

// serveConsoleAsset writes an embedded asset, negotiating gzip and answering
// conditional requests. cacheControl is passed through so the caller keeps
// control of revalidation policy.
func serveConsoleAsset(w http.ResponseWriter, r *http.Request, asset *consoleAsset, cacheControl string) {
	body := asset.raw
	etag := asset.etag
	useGzip := asset.gzipped != nil && clientAcceptsGzip(r.Header.Get("Accept-Encoding"))
	if useGzip {
		body = asset.gzipped
		etag = asset.gzipETag
	}

	header := w.Header()
	header.Set("Content-Type", asset.contentType)
	if cacheControl != "" {
		header.Set("Cache-Control", cacheControl)
	}
	header.Set("ETag", etag)
	// Caches must key on the encoding, otherwise a gzip response can be replayed
	// to a client that never asked for it.
	header.Set("Vary", "Accept-Encoding")
	if useGzip {
		header.Set("Content-Encoding", "gzip")
	}

	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		// A 304 carries no body, so drop headers that would describe one.
		header.Del("Content-Type")
		header.Del("Content-Encoding")
		w.WriteHeader(http.StatusNotModified)
		return
	}

	header.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// clientAcceptsGzip reports whether the header advertises gzip without
// explicitly disabling it via q=0.
func clientAcceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		if coding != "gzip" && coding != "*" {
			continue
		}
		acceptable := true
		for _, parameter := range fields[1:] {
			parameter = strings.ToLower(strings.TrimSpace(parameter))
			if !strings.HasPrefix(parameter, "q=") {
				continue
			}
			if quality, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64); err == nil && quality == 0 {
				acceptable = false
			}
		}
		if acceptable {
			return true
		}
	}
	return false
}

// matchesETag compares an If-None-Match header against the tag being served,
// tolerating weak validators and comma-separated lists.
func matchesETag(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" || etag == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}
