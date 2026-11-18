package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func snapshotCall(t *testing.T, args map[string]any) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": "browser_snapshot", "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	return callTool(params)
}

func TestSnapshotRejectsInvalidViewportBeforeLaunch(t *testing.T) {
	for _, key := range []string{"viewport_width", "viewport_height"} {
		for _, value := range []any{0, -1, 319, 4097, 1e100, 640.5, "640", nil, true} {
			t.Run(fmt.Sprintf("%s_%v", key, value), func(t *testing.T) {
				result := snapshotCall(t, map[string]any{"url": "http://127.0.0.1:1", "allowed_hosts": []string{"127.0.0.1"}, "authorized_scope": true, key: value})
				if result["is_error"] != true || !strings.Contains(fmt.Sprint(result["content"]), key) {
					t.Fatalf("expected viewport rejection, got %v", result)
				}
			})
		}
	}
}

func TestSnapshotRejectsUnauthorizedTargets(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	for _, args := range []map[string]any{
		{"url": server.URL, "allowed_hosts": []string{"127.0.0.1"}, "authorized_scope": false},
		{"url": server.URL, "allowed_hosts": []string{"localhost"}, "authorized_scope": true},
		{"url": server.URL, "allowed_hosts": []string{}, "authorized_scope": true},
		{"url": "file:///fixture.html", "allowed_hosts": []string{"127.0.0.1"}, "authorized_scope": true},
		{"url": server.URL, "allowed_hosts": []string{"127.0.0.1"}, "authorized_scope": true, "expression": "alert(1)"},
	} {
		if result := snapshotCall(t, args); result["is_error"] != true {
			t.Fatalf("expected rejection: %v", result)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("rejected calls made %d HTTP requests", hits.Load())
	}
}

func TestSnapshotViewportDefaultsAndSchema(t *testing.T) {
	for _, tc := range []struct {
		args          map[string]any
		width, height int
	}{
		{nil, 1365, 768},
		{map[string]any{"viewport_width": float64(320)}, 320, 768},
		{map[string]any{"viewport_height": float64(4096)}, 1365, 4096},
	} {
		width, height, err := snapshotViewport(tc.args)
		if err != nil || width != tc.width || height != tc.height {
			t.Fatalf("viewport = %dx%d, %v", width, height, err)
		}
	}
	for _, tool := range builtinTools() {
		if tool.Name != "browser_snapshot" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Type                      string
				Minimum, Maximum, Default int
			}
			Required             []string
			AdditionalProperties bool
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		for key, expected := range map[string]int{"viewport_width": 1365, "viewport_height": 768} {
			p := schema.Properties[key]
			if p.Type != "integer" || p.Minimum != 320 || p.Maximum != 4096 || p.Default != expected {
				t.Fatalf("incorrect schema for %s: %+v", key, p)
			}
		}
		if schema.AdditionalProperties || strings.Contains(strings.Join(schema.Required, ","), "viewport") {
			t.Fatal("viewport must be optional and extra arguments forbidden")
		}
		return
	}
	t.Fatal("browser_snapshot missing")
}

func TestSnapshotCDPPipeRejectsMalformedAndOversizedOutput(t *testing.T) {
	for _, data := range []string{`{"id":1}`, "broken\x00", strings.Repeat("x", maxCDPMessageBytes)} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		messages := make(chan snapshotCDPMessage)
		go readSnapshotCDP(ctx, strings.NewReader(data), messages)
		message := <-messages
		cancel()
		if message.err == nil {
			t.Fatal("expected bounded protocol error")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := snapshotCDP{ctx: ctx, messages: make(chan snapshotCDPMessage)}
	if _, err := c.next(); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestSnapshotCDPPipeHandlesEventsBetweenReplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	input, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	var commands bytes.Buffer
	messages := make(chan snapshotCDPMessage)
	go readSnapshotCDP(ctx, input, messages)
	c := snapshotCDP{ctx: ctx, input: &commands, messages: messages, session: "page", allowedHosts: []string{"127.0.0.1"}}
	go func() {
		fmt.Fprint(output, "{\"method\":\"Page.lifecycleEvent\",\"sessionId\":\"page\",\"params\":{\"name\":\"DOMContentLoaded\",\"loaderId\":\"fixture\"}}\x00")
		fmt.Fprint(output, "{\"method\":\"Fetch.requestPaused\",\"sessionId\":\"page\",\"params\":{\"requestId\":\"asset\",\"resourceType\":\"Script\",\"request\":{\"url\":\"http://localhost/blocked.js\"}}}\x00")
		fmt.Fprint(output, "{\"id\":1,\"result\":{\"ok\":true}}\x00")
	}()
	var reply map[string]bool
	if err := c.call("Page.enable", map[string]any{}, &reply); err != nil || !reply["ok"] || !c.loaded["fixture"] {
		t.Fatalf("interleaved reply failed: %v %v", reply, err)
	}
	if !strings.Contains(commands.String(), "Fetch.failRequest") {
		t.Fatal("unauthorized subresource was not blocked")
	}
}

func requireSnapshotBrowser(t *testing.T) {
	t.Helper()
	if os.Getenv("GOFLOW_BROWSER_INTEGRATION") != "1" {
		t.Skip("set GOFLOW_BROWSER_INTEGRATION=1 to use an installed Chromium/Edge browser")
	}
	if _, err := resolveBrowserPath(); err != nil {
		t.Skip(err)
	}
	t.Setenv("GOFLOW_WORKSPACE_ROOT", t.TempDir())
	// All transient profiles from this test must be removable, also on Windows.
	profileRoot := t.TempDir()
	t.Setenv("TMP", profileRoot)
	t.Setenv("TEMP", profileRoot)
	t.Setenv("TMPDIR", profileRoot)
	t.Cleanup(func() {
		entries, err := os.ReadDir(profileRoot)
		if err != nil {
			t.Error(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "goflow-browser-metrics-") {
				t.Errorf("browser profile leaked: %s", entry.Name())
			}
		}
	})
}

// Opt in so ordinary unit tests need neither a browser nor external services.
func TestSnapshotLocalBrowserIntegration(t *testing.T) {
	requireSnapshotBrowser(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Local layout fixture</title><style>
html,body{margin:0} #wide{width:900px;height:1200px} #motion{animation:move 10s linear infinite}
@keyframes move{from{opacity:.4}to{opacity:1}} @media(min-width:1000px){#wide{width:100%}}
@media(prefers-reduced-motion:reduce){#motion{animation:none}}</style></head>
<body><div id="wide"><span id="motion">safe local fixture</span></div><script>
document.getElementById('wide').style.height='1500px';
document.body.animate([{opacity:1},{opacity:.9}],{duration:10000,iterations:Infinity}).pause();
window.matchMedia=()=>({matches:'spoofed'});
</script></body></html>`)
	}))
	defer server.Close()
	for _, size := range []struct {
		width, height int
		overflow      bool
	}{{640, 480, true}, {1365, 768, false}, {320, 320, true}, {4096, 4096, false}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			args := map[string]any{"url": server.URL, "allowed_hosts": []string{"127.0.0.1"}, "authorized_scope": true, "wait_ms": 500, "timeout_seconds": 15, "screenshot": true}
			if size.width != 1365 {
				args["viewport_width"] = size.width
				args["viewport_height"] = size.height
			}
			result := snapshotCall(t, args)
			if result["is_error"] == true {
				t.Fatal(result["content"])
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
				t.Fatal(err)
			}
			layout := payload["layout_metrics"].(map[string]any)
			if layout["inner_width"] != float64(size.width) || layout["inner_height"] != float64(size.height) || layout["horizontal_overflow"] != size.overflow {
				t.Fatalf("unexpected actual layout: %v", layout)
			}
			doc := layout["document_element"].(map[string]any)
			if doc["scroll_height"].(float64) < 1500 || doc["client_width"].(float64) > float64(size.width) {
				t.Fatalf("wrong DOM sizes: %v", doc)
			}
			if payload["title"] != "Local layout fixture" || payload["dom_sha256"] == "" {
				t.Fatalf("legacy evidence missing: %v", payload)
			}
			motion := payload["motion"].(map[string]any)
			if motion["animations_supported"] != true {
				t.Fatalf("missing animation support: %v", motion)
			}
			if _, ok := motion["prefers_reduced_motion"].(bool); !ok {
				t.Fatalf("page script changed isolated measurement: %v", motion)
			}
			if motion["play_states"].(map[string]any)["paused"].(float64) != 1 {
				t.Fatalf("missing paused Web Animation: %v", motion)
			}
			if motion["prefers_reduced_motion"] == false && motion["animation_count"].(float64) < 1 {
				t.Fatalf("missing animation: %v", motion)
			}
			if payload["screenshot"] == nil {
				t.Fatal("missing screenshot")
			}
			shot := payload["screenshot"].(map[string]any)
			file, err := os.Open(shot["path"].(string))
			if err != nil {
				t.Fatal(err)
			}
			config, err := png.DecodeConfig(bufio.NewReader(file))
			file.Close()
			if err != nil || config.Width != size.width || config.Height != size.height {
				t.Fatalf("wrong screenshot dimensions: %v, %v", config, err)
			}
			if shot["sha256"] != fileSHA256(shot["path"].(string)) || shot["bytes"].(float64) <= 0 {
				t.Fatalf("invalid screenshot evidence: %v", shot)
			}
			if len(result["content"].(string)) > 30000 {
				t.Fatal("fixture output unexpectedly large")
			}
		})
	}
}

func TestSnapshotLocalBrowserRejectsRedirectAndBoundsWork(t *testing.T) {
	requireSnapshotBrowser(t)
	var blockedHits atomic.Int32
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { blockedHits.Add(1) }))
	defer blocked.Close()
	blockedURL := strings.Replace(blocked.URL, "127.0.0.1", "localhost", 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, blockedURL, http.StatusFound)
		case "/oversized":
			fmt.Fprint(w, "<!doctype html><body>"+strings.Repeat("x", maxSnapshotDOMChars)+"</body>")
		case "/hang":
			<-r.Context().Done()
		case "/asset":
			fmt.Fprintf(w, `<!doctype html><title>blocked asset</title><script src="%s/blocked.js"></script><p>local</p>`, blockedURL)
		case "/frame":
			fmt.Fprintf(w, `<!doctype html><title>blocked frame</title><iframe src="%s/frame"></iframe>`, blockedURL)
		}
	}))
	defer server.Close()
	for _, tc := range []struct{ path, expected string }{
		{"/redirect", "allowed_hosts"}, {"/oversized", "DOM exceeds"}, {"/hang", "deadline"}, {"/asset", ""}, {"/frame", "allowed_hosts"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			start := time.Now()
			result := snapshotCall(t, map[string]any{"url": server.URL + tc.path, "allowed_hosts": []string{"127.0.0.1"}, "authorized_scope": true, "wait_ms": 500, "timeout_seconds": 2})
			if tc.expected == "" {
				if result["is_error"] == true {
					t.Fatal(result)
				}
			} else if result["is_error"] != true || !strings.Contains(fmt.Sprint(result["content"]), tc.expected) {
				t.Fatalf("expected %s failure: %v", tc.expected, result)
			}
			if time.Since(start) > 6*time.Second {
				t.Fatal("browser exceeded timeout and cleanup budget")
			}
		})
	}
	if blockedHits.Load() != 0 {
		t.Fatalf("unauthorized host received %d requests", blockedHits.Load())
	}
	files, err := filepath.Glob(filepath.Join(browserEvidenceDir(), "*.png"))
	if err != nil || len(files) != 0 {
		t.Fatalf("unexpected screenshot files: %v %v", files, err)
	}
}
