package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConsoleAssetServesGzipWhenAccepted(t *testing.T) {
	handler := (&Server{}).handleConsoleAssets()
	request := httptest.NewRequest(http.MethodGet, "/assets/styles.css", nil)
	request.Header.Set("Accept-Encoding", "gzip, deflate")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want %q", got, "gzip")
	}
	if got := recorder.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want %q", got, "Accept-Encoding")
	}

	reader, err := gzip.NewReader(bytes.NewReader(recorder.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	raw, err := consoleFS.ReadFile("web/assets/styles.css")
	if err != nil {
		t.Fatalf("read embedded asset: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatal("decompressed body does not match the embedded asset")
	}
	if recorder.Body.Len() >= len(raw) {
		t.Fatalf("gzip body %d bytes is not smaller than raw %d bytes", recorder.Body.Len(), len(raw))
	}
}

func TestConsoleAssetServesRawWithoutGzipSupport(t *testing.T) {
	handler := (&Server{}).handleConsoleAssets()
	request := httptest.NewRequest(http.MethodGet, "/assets/styles.css", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	raw, err := consoleFS.ReadFile("web/assets/styles.css")
	if err != nil {
		t.Fatalf("read embedded asset: %v", err)
	}
	if !bytes.Equal(recorder.Body.Bytes(), raw) {
		t.Fatal("body does not match the embedded asset")
	}
}

func TestConsoleAssetRejectsGzipWithZeroQuality(t *testing.T) {
	handler := (&Server{}).handleConsoleAssets()
	request := httptest.NewRequest(http.MethodGet, "/assets/styles.css", nil)
	request.Header.Set("Accept-Encoding", "gzip;q=0, identity")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for gzip;q=0", got)
	}
}

func TestConsoleAssetConditionalRequestReturnsNotModified(t *testing.T) {
	handler := (&Server{}).handleConsoleAssets()
	for _, encoding := range []string{"", "gzip"} {
		first := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
		if encoding != "" {
			first.Header.Set("Accept-Encoding", encoding)
		}
		firstRecorder := httptest.NewRecorder()
		handler.ServeHTTP(firstRecorder, first)

		etag := firstRecorder.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("encoding %q: missing ETag on first response", encoding)
		}

		second := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
		if encoding != "" {
			second.Header.Set("Accept-Encoding", encoding)
		}
		second.Header.Set("If-None-Match", etag)
		secondRecorder := httptest.NewRecorder()
		handler.ServeHTTP(secondRecorder, second)

		if secondRecorder.Code != http.StatusNotModified {
			t.Fatalf("encoding %q: status = %d, want %d", encoding, secondRecorder.Code, http.StatusNotModified)
		}
		if secondRecorder.Body.Len() != 0 {
			t.Fatalf("encoding %q: 304 body should be empty, got %d bytes", encoding, secondRecorder.Body.Len())
		}
	}
}

// A gzip ETag must not satisfy a raw request, otherwise a client can end up
// caching a compressed body it cannot decode.
func TestConsoleAssetETagDistinguishesEncoding(t *testing.T) {
	handler := (&Server{}).handleConsoleAssets()
	gzipRequest := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	gzipRequest.Header.Set("Accept-Encoding", "gzip")
	gzipRecorder := httptest.NewRecorder()
	handler.ServeHTTP(gzipRecorder, gzipRequest)
	gzipETag := gzipRecorder.Header().Get("ETag")

	rawRequest := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rawRequest.Header.Set("If-None-Match", gzipETag)
	rawRecorder := httptest.NewRecorder()
	handler.ServeHTTP(rawRecorder, rawRequest)

	if rawRecorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d for a gzip ETag on a raw request", rawRecorder.Code, http.StatusOK)
	}
}

func TestConsoleAssetRejectsPathTraversal(t *testing.T) {
	handler := (&Server{}).handleConsoleAssets()
	for _, target := range []string{
		"/assets/../console.html",
		"/assets/views/../../console.html",
		"/assets/",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want %d", target, recorder.Code, http.StatusNotFound)
		}
	}
}

func TestConsoleAssetServesNestedViewModules(t *testing.T) {
	handler := (&Server{}).handleConsoleAssets()
	request := httptest.NewRequest(http.MethodGet, "/assets/views/chat.js", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want JavaScript", got)
	}
}

func TestConsoleAssetHeadRequestOmitsBody(t *testing.T) {
	handler := (&Server{}).handleConsoleAssets()
	request := httptest.NewRequest(http.MethodHead, "/assets/styles.css", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("HEAD body should be empty, got %d bytes", recorder.Body.Len())
	}
	if recorder.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD response should still declare Content-Length")
	}
}

func TestHandleConsoleServesCompressedHTML(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/console", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	recorder := httptest.NewRecorder()

	server.handleConsole(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want HTML", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestClientAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                         false,
		"identity":                 false,
		"gzip":                     true,
		"GZIP":                     true,
		"deflate, gzip;q=0.8":      true,
		"gzip;q=0":                 false,
		"*":                        true,
		"*;q=0":                    false,
		"br, zstd":                 false,
		" gzip , deflate ":         true,
		"identity;q=1, gzip;q=0.5": true,
	}
	for header, want := range cases {
		if got := clientAcceptsGzip(header); got != want {
			t.Errorf("clientAcceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestMatchesETag(t *testing.T) {
	cases := []struct {
		header string
		etag   string
		want   bool
	}{
		{"", `"abc"`, false},
		{`"abc"`, `"abc"`, true},
		{`W/"abc"`, `"abc"`, true},
		{`"xyz", "abc"`, `"abc"`, true},
		{`"xyz"`, `"abc"`, false},
		{"*", `"abc"`, true},
		{`"abc"`, "", false},
	}
	for _, testCase := range cases {
		if got := matchesETag(testCase.header, testCase.etag); got != testCase.want {
			t.Errorf("matchesETag(%q, %q) = %v, want %v", testCase.header, testCase.etag, got, testCase.want)
		}
	}
}
