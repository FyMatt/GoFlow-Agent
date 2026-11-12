package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const maxSnapshotDOMChars = 256 * 1024
const maxSnapshotPNGBytes = 8 * 1024 * 1024
const maxCDPMessageBytes = 12 * 1024 * 1024
const maxCDPTotalBytes = 32 * 1024 * 1024

func snapshotViewport(args map[string]any) (int, int, error) {
	for key := range args {
		switch key {
		case "url", "allowed_hosts", "authorized_scope", "wait_ms", "timeout_seconds", "max_dom_chars", "screenshot", "viewport_width", "viewport_height":
		default:
			return 0, 0, fmt.Errorf("unknown browser_snapshot argument %q", key)
		}
	}
	width, height := 1365, 768
	for key, dest := range map[string]*int{"viewport_width": &width, "viewport_height": &height} {
		if raw, exists := args[key]; exists {
			value, ok := raw.(float64) // tools/call arguments are decoded from JSON.
			if !ok || value < 320 || value > 4096 || math.Trunc(value) != value {
				return 0, 0, fmt.Errorf("%s must be an integer between 320 and 4096 CSS pixels", key)
			}
			*dest = int(value)
		}
	}
	return width, height, nil
}

type snapshotCapture struct {
	DOM      string         `json:"dom"`
	URL      string         `json:"url"`
	TooLarge bool           `json:"too_large"`
	Layout   map[string]any `json:"layout"`
	Motion   map[string]any `json:"motion"`
}

// Evaluated only in a fresh isolated world. No model-provided expression,
// selector, or script is accepted. Measurements describe this document only.
const snapshotMetricsScript = `(() => {
  const root = document.documentElement, body = document.body;
  const sizes = e => e ? ({client_width:e.clientWidth, client_height:e.clientHeight,
    scroll_width:e.scrollWidth, scroll_height:e.scrollHeight}) : null;
  const scrolling = document.scrollingElement || root;
  const animationsSupported = typeof document.getAnimations === 'function';
  const states = {running:0, paused:0, finished:0, idle:0};
  const animations = animationsSupported ? document.getAnimations() : [];
  for (const a of animations) if (a.playState in states) states[a.playState]++;
  const dom = root ? root.outerHTML : '';
  return {url:location.href, dom:dom.length <= 262144 ? dom : '', too_large:dom.length > 262144,
    layout:{units:'css_pixels', inner_width:innerWidth, inner_height:innerHeight,
      device_pixel_ratio:devicePixelRatio, document_element:sizes(root), body:sizes(body),
      scrolling_element:sizes(scrolling), horizontal_overflow:!!scrolling && scrolling.scrollWidth > scrolling.clientWidth},
    motion:{prefers_reduced_motion:matchMedia('(prefers-reduced-motion: reduce)').matches,
      animations_supported:animationsSupported, animation_count:animations.length, play_states:states}};
})()`

type snapshotCDPMessage struct {
	ID        int             `json:"id"`
	SessionID string          `json:"sessionId"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`
	err       error
}

type snapshotCDP struct {
	ctx          context.Context
	input        io.Writer
	messages     <-chan snapshotCDPMessage
	seq          int
	session      string
	allowedHosts []string
	loaded       map[string]bool // DOMContentLoaded loader IDs, including early events.
}

func splitCDPMessage(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) != 0 {
		return 0, nil, fmt.Errorf("incomplete CDP pipe message")
	}
	return 0, nil, nil
}

func readSnapshotCDP(ctx context.Context, output io.Reader, messages chan<- snapshotCDPMessage) {
	defer close(messages)
	scanner := bufio.NewScanner(io.LimitReader(output, maxCDPTotalBytes))
	scanner.Buffer(make([]byte, 64*1024), maxCDPMessageBytes)
	scanner.Split(splitCDPMessage)
	for scanner.Scan() {
		var message snapshotCDPMessage
		message.err = json.Unmarshal(scanner.Bytes(), &message)
		select {
		case messages <- message:
		case <-ctx.Done():
			return
		}
		if message.err != nil {
			return
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	select {
	case messages <- snapshotCDPMessage{err: err}:
	case <-ctx.Done():
	}
}

func (c *snapshotCDP) send(method string, params any, session string) (int, error) {
	c.seq++
	data, err := json.Marshal(map[string]any{"id": c.seq, "method": method, "params": params, "sessionId": session})
	if err == nil {
		_, err = c.input.Write(append(data, 0))
	}
	return c.seq, err
}

func (c *snapshotCDP) next() (snapshotCDPMessage, error) {
	select {
	case <-c.ctx.Done():
		return snapshotCDPMessage{}, c.ctx.Err()
	case message, ok := <-c.messages:
		if !ok {
			message.err = io.EOF
		}
		return message, c.handle(message)
	}
}

func (c *snapshotCDP) handle(message snapshotCDPMessage) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if message.err != nil {
		return message.err
	}
	if message.Method == "Page.lifecycleEvent" && message.SessionID == c.session {
		var event struct{ Name, LoaderID string }
		if err := json.Unmarshal(message.Params, &event); err != nil {
			return err
		}
		if event.Name == "DOMContentLoaded" {
			if c.loaded == nil {
				c.loaded = make(map[string]bool)
			}
			c.loaded[event.LoaderID] = true
		}
	}
	if message.Method == "Fetch.requestPaused" {
		var paused struct {
			RequestID    string `json:"requestId"`
			ResourceType string `json:"resourceType"`
			Request      struct {
				URL string `json:"url"`
			} `json:"request"`
		}
		if err := json.Unmarshal(message.Params, &paused); err != nil {
			return err
		}
		parsed, err := validateHTTPURL(paused.Request.URL)
		allowed := err == nil && hostAllowed(parsed.Hostname(), c.allowedHosts)
		method := "Fetch.continueRequest"
		params := map[string]any{"requestId": paused.RequestID}
		if !allowed {
			method = "Fetch.failRequest"
			params["errorReason"] = "BlockedByClient"
		}
		if _, err := c.send(method, params, message.SessionID); err != nil {
			return err
		}
		if !allowed && paused.ResourceType == "Document" {
			return fmt.Errorf("document navigation outside allowed_hosts or HTTP(S) blocked")
		}
	}
	return nil
}

func (c *snapshotCDP) call(method string, params any, result any) error {
	id, err := c.send(method, params, c.session)
	if err != nil {
		return err
	}
	for {
		message, err := c.next()
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		if message.ID != id {
			continue
		}
		if len(message.Error) != 0 {
			return fmt.Errorf("%s: %s", method, compactText(string(message.Error), 500))
		}
		if result != nil {
			return json.Unmarshal(message.Result, result)
		}
		return nil
	}
}

// wait continues servicing intercepted requests while the page settles. Both
// this wait and every CDP operation share the caller's overall deadline.
func (c *snapshotCDP) wait(duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return nil
		case <-c.ctx.Done():
			return c.ctx.Err()
		case message, ok := <-c.messages:
			if !ok {
				message.err = io.EOF
			}
			if err := c.handle(message); err != nil {
				return err
			}
		}
	}
}

func captureBrowserSnapshot(ctx context.Context, browser, targetURL string, allowedHosts []string, waitMS int, screenshot bool, width, height int) (capture snapshotCapture, screenshotPath string, resultErr error) {
	if width < 320 || width > 4096 || height < 320 || height > 4096 {
		return capture, "", fmt.Errorf("viewport dimensions must be between 320 and 4096 CSS pixels")
	}
	if err := ctx.Err(); err != nil {
		return capture, "", err
	}
	profile, err := os.MkdirTemp("", "goflow-browser-metrics-*")
	if err != nil {
		return capture, "", err
	}
	defer func() {
		// Windows descendants may briefly hold profile files after the parent exits.
		deadline := time.Now().Add(time.Second)
		for {
			err := os.RemoveAll(profile)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove browser profile: %w", err))
				if screenshotPath != "" {
					os.Remove(screenshotPath)
				}
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	childInput, input, err := os.Pipe()
	if err != nil {
		return capture, "", err
	}
	defer childInput.Close()
	defer input.Close()
	output, childOutput, err := os.Pipe()
	if err != nil {
		return capture, "", err
	}
	defer output.Close()
	defer childOutput.Close()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return capture, "", err
	}
	defer devNull.Close()
	// On cancellation, close the CDP pipes first so Chromium shuts down its
	// children; killing just the parent can leave descendants holding the profile.
	cmd := exec.Command(browser, "--headless=new", "--disable-gpu", "--disable-dev-shm-usage",
		"--disable-extensions", "--disable-background-networking", "--no-first-run", "--no-default-browser-check",
		"--remote-debugging-pipe", "--user-data-dir="+profile, fmt.Sprintf("--window-size=%d,%d", width, height), "about:blank")
	cmd.Stdout, cmd.Stderr = devNull, devNull
	if err := configureBrowserPipes(cmd, childInput, childOutput); err != nil {
		return capture, "", err
	}
	if err := cmd.Start(); err != nil {
		return capture, "", fmt.Errorf("start headless browser: %w", err)
	}
	childInput.Close()
	childOutput.Close()
	pipeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopClose := context.AfterFunc(pipeCtx, func() { input.Close(); output.Close() })
	defer stopClose()
	messages := make(chan snapshotCDPMessage)
	readerDone := make(chan struct{})
	go func() { defer close(readerDone); readSnapshotCDP(pipeCtx, output, messages) }()
	c := &snapshotCDP{ctx: pipeCtx, input: input, messages: messages, allowedHosts: allowedHosts}
	processDone := make(chan error, 1)
	go func() { processDone <- cmd.Wait() }()
	defer func() {
		// Closing the CDP pipe asks Chromium to exit, including its children.
		// Also request an orderly close so profile files are released promptly.
		if ctx.Err() == nil {
			c.send("Browser.close", map[string]any{}, "")
		}
		input.Close()
		select {
		case <-processDone:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
			<-processDone
		}
		cancel()
		output.Close()
		<-readerDone
	}()
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := c.call("Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
		return capture, "", err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call("Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, &attached); err != nil {
		return capture, "", err
	}
	c.session = attached.SessionID
	for _, op := range []struct {
		method string
		params map[string]any
	}{
		{"Page.enable", map[string]any{}},
		{"Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
		{"Network.enable", map[string]any{}},
		{"Network.setBypassServiceWorker", map[string]any{"bypass": true}},
		{"Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}}}},
		{"Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false}},
		{"Browser.setDownloadBehavior", map[string]any{"behavior": "deny"}},
	} {
		if err := c.call(op.method, op.params, nil); err != nil {
			return capture, "", err
		}
	}
	var nav struct {
		ErrorText string `json:"errorText"`
		LoaderID  string `json:"loaderId"`
	}
	if err := c.call("Page.navigate", map[string]any{"url": targetURL}, &nav); err != nil {
		return capture, "", err
	}
	if nav.ErrorText != "" {
		return capture, "", fmt.Errorf("browser navigation: %s", nav.ErrorText)
	}
	for !c.loaded[nav.LoaderID] {
		if _, err := c.next(); err != nil {
			return capture, "", err
		}
	}
	if err := c.wait(time.Duration(waitMS) * time.Millisecond); err != nil {
		return capture, "", err
	}
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := c.call("Page.getFrameTree", map[string]any{}, &tree); err != nil {
		return capture, "", err
	}
	var world struct {
		ExecutionContextID int `json:"executionContextId"`
	}
	if err := c.call("Page.createIsolatedWorld", map[string]any{"frameId": tree.FrameTree.Frame.ID, "worldName": "goflow-snapshot"}, &world); err != nil {
		return capture, "", err
	}
	var evaluated struct {
		Result struct {
			Value snapshotCapture `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	if err := c.call("Runtime.evaluate", map[string]any{"expression": snapshotMetricsScript, "contextId": world.ExecutionContextID, "returnByValue": true, "timeout": 2000}, &evaluated); err != nil {
		return capture, "", err
	}
	if len(evaluated.Exception) != 0 {
		return capture, "", fmt.Errorf("built-in layout measurement script failed")
	}
	capture = evaluated.Result.Value
	if capture.TooLarge {
		return capture, "", fmt.Errorf("browser DOM exceeds %d characters", maxSnapshotDOMChars)
	}
	final, err := validateHTTPURL(capture.URL)
	if err != nil || !hostAllowed(final.Hostname(), allowedHosts) {
		return capture, "", fmt.Errorf("final document URL is outside allowed_hosts or HTTP(S)")
	}
	if capture.DOM == "" || capture.Layout == nil {
		return capture, "", fmt.Errorf("browser returned no document measurements")
	}
	if !screenshot {
		return capture, "", nil
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := c.call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false}, &shot); err != nil {
		return capture, "", err
	}
	if len(shot.Data) > base64.StdEncoding.EncodedLen(maxSnapshotPNGBytes) {
		return capture, "", fmt.Errorf("browser screenshot exceeds %d bytes", maxSnapshotPNGBytes)
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return capture, "", err
	}
	if len(png) > maxSnapshotPNGBytes {
		return capture, "", fmt.Errorf("browser screenshot exceeds %d bytes", maxSnapshotPNGBytes)
	}
	dir := browserEvidenceDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return capture, "", err
	}
	file, err := os.CreateTemp(dir, "browser-*.png")
	if err != nil {
		return capture, "", err
	}
	path := filepath.Clean(file.Name())
	_, writeErr := file.Write(png)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(path)
		return capture, "", fmt.Errorf("write browser screenshot: %v / %v", writeErr, closeErr)
	}
	return capture, path, nil
}
