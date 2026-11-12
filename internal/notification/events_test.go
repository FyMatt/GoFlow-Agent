package notification

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testInput() Input {
	return Input{Severity: "warning", Title: "Task needs review", Summary: "Review the recorded evidence.", EvidenceRefs: []string{"artifact://run/report.json"}}
}

func saveProfile(t *testing.T, root, id string, profile any) {
	t.Helper()
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "notifications", "channels", id+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func reloadEvent(t *testing.T, root, id string) Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "notifications", "outbox", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestLocalPersistenceReload(t *testing.T) {
	root := t.TempDir()
	p := Publisher{DataRoot: root, NetworkDisabled: true}
	// Even a saved profile cannot override the reserved local collector.
	saveProfile(t, root, "local", Channel{Kind: "webhook", URL: "invalid"})
	event, err := p.Publish(t.Context(), testInput())
	if err != nil || event.EventID == "" || event.DeliveryStatus != "stored" || event.ChannelID != "local" {
		t.Fatalf("publish: %+v, %v", event, err)
	}
	if loaded := reloadEvent(t, root, event.EventID); !reflect.DeepEqual(event, loaded) {
		t.Fatalf("reload mismatch: %+v", loaded)
	}
	// A fresh publisher retains previous records and generates a distinct ID.
	other, err := (Publisher{DataRoot: root}).Publish(t.Context(), testInput())
	if err != nil || other.EventID == event.EventID {
		t.Fatalf("second publish: %+v, %v", other, err)
	}
	reloadEvent(t, root, event.EventID)
}

func TestInvalidChannelsPersistFailure(t *testing.T) {
	cases := []struct {
		name    string
		profile any
	}{
		{"missing", nil},
		{"wrong-kind", Channel{Kind: "email", URL: "http://127.0.0.1"}},
		{"invalid-url", Channel{Kind: "webhook", URL: ":invalid"}},
		{"scheme", Channel{Kind: "webhook", URL: "file:///tmp/hook"}},
		{"userinfo", Channel{Kind: "webhook", URL: "http://user:PRIVATE_TOKEN@127.0.0.1"}},
		{"query", Channel{Kind: "webhook", URL: "http://127.0.0.1/?token=PRIVATE_TOKEN"}},
		{"fragment", Channel{Kind: "webhook", URL: "http://127.0.0.1/#PRIVATE_TOKEN"}},
		{"private", Channel{Kind: "webhook", URL: "http://127.0.0.1"}},
		{"ipv6-loopback", Channel{Kind: "webhook", URL: "http://[::1]"}},
		{"unspecified", Channel{Kind: "webhook", URL: "http://0.0.0.0"}},
		{"inline-token", map[string]any{"kind": "webhook", "url": "http://127.0.0.1", "token": "PRIVATE_TOKEN"}},
		{"inline-credential-ref", Channel{Kind: "webhook", URL: "http://127.0.0.1", AllowPrivate: true, CredentialRef: "PRIVATE_TOKEN"}},
		{"missing-secret", Channel{Kind: "webhook", URL: "http://127.0.0.1", AllowPrivate: true, CredentialRef: "secret://notifications/missing"}},
		{"traversal-secret", Channel{Kind: "webhook", URL: "http://127.0.0.1", AllowPrivate: true, CredentialRef: "secret://notifications/../PRIVATE_TOKEN"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.profile != nil {
				saveProfile(t, root, "external", tc.profile)
			}
			input := testInput()
			input.ChannelID = "external"
			event, err := (Publisher{DataRoot: root}).Publish(t.Context(), input)
			if err == nil || event.DeliveryStatus != "failed" || event.EventID == "" {
				t.Fatalf("expected persistent failure: %+v, %v", event, err)
			}
			loaded := reloadEvent(t, root, event.EventID)
			data, _ := json.Marshal(loaded)
			if !reflect.DeepEqual(event, loaded) || strings.Contains(string(data), "PRIVATE_TOKEN") || strings.Contains(string(data), "127.0.0.1") {
				t.Fatalf("unexpected persisted record: %s", data)
			}
		})
	}
}

func TestWebhookDelivery(t *testing.T) {
	for _, status := range []int{200, 202, 204, 302, 400, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			root := t.TempDir()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var received Event
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("Idempotency-Key") != received.EventID || received.Title != testInput().Title || !reflect.DeepEqual(received.EvidenceRefs, testInput().EvidenceRefs) {
					t.Error("incorrect request payload or headers")
				}
				// Durability is established before any outbound request.
				if saved := reloadEvent(t, root, received.EventID); saved.DeliveryStatus != "pending" {
					t.Errorf("expected pending record before delivery: %+v", saved)
				}
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("fixture-secret must never be persisted"))
			}))
			defer server.Close()
			secretPath := filepath.Join(root, "secrets", "notifications", "test-token")
			if err := os.MkdirAll(filepath.Dir(secretPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(secretPath, []byte("fixture-secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			saveProfile(t, root, "test", Channel{Kind: "webhook", URL: server.URL, AllowPrivate: true, CredentialRef: "secret://notifications/test-token"})
			input := testInput()
			input.ChannelID = "test"
			event, err := (Publisher{DataRoot: root}).Publish(t.Context(), input)
			want := "delivered"
			if status >= 300 {
				want = "failed"
			}
			if event.DeliveryStatus != want || event.HTTPStatus != status || (err != nil) != (status >= 300) || calls.Load() != 1 {
				t.Fatalf("delivery: %+v, %v, calls=%d", event, err, calls.Load())
			}
			loaded := reloadEvent(t, root, event.EventID)
			data, _ := json.Marshal(loaded)
			if !reflect.DeepEqual(event, loaded) || strings.Contains(string(data), "fixture-secret") {
				t.Fatalf("persisted outcome mismatch or secret leak: %s", data)
			}
		})
	}
}

func TestNetworkDisabled(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	root := t.TempDir()
	saveProfile(t, root, "test", Channel{Kind: "webhook", URL: server.URL, AllowPrivate: true})
	input := testInput()
	input.ChannelID = "test"
	event, err := (Publisher{DataRoot: root, NetworkDisabled: true}).Publish(t.Context(), input)
	if err == nil || event.DeliveryStatus != "failed" || calls.Load() != 0 {
		t.Fatalf("network restriction bypass: %+v, %v", event, err)
	}
}

func TestWebhookTransportFailures(t *testing.T) {
	for _, oversizedHeader := range []bool{false, true} {
		root := t.TempDir()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Too-Large", strings.Repeat("x", 20000))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		if !oversizedHeader {
			server.Close() // A closed loopback listener exercises connection failure.
		}
		saveProfile(t, root, "test", Channel{Kind: "webhook", URL: server.URL + "/PRIVATE_TOKEN", AllowPrivate: true})
		input := testInput()
		input.ChannelID = "test"
		event, err := (Publisher{DataRoot: root}).Publish(t.Context(), input)
		if err == nil || event.DeliveryStatus != "failed" || event.HTTPStatus != 0 {
			t.Fatalf("transport failure reported as success: %+v, %v", event, err)
		}
		loaded := reloadEvent(t, root, event.EventID)
		if loaded.DeliveryStatus != "failed" || strings.Contains(loaded.Error, "PRIVATE_TOKEN") || strings.Contains(err.Error(), server.URL) {
			t.Fatalf("unsanitized transport error: %+v, %v", loaded, err)
		}
	}
}

func TestCredentialsCannotUsePublicPlainHTTP(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secrets", "notifications", "token")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This documentation-only literal is rejected before any dial, even when
	// allow_private is true. No non-loopback request is made by this test.
	saveProfile(t, root, "test", Channel{Kind: "webhook", URL: "http://192.0.2.1", AllowPrivate: true, CredentialRef: "secret://notifications/token"})
	input := testInput()
	input.ChannelID = "test"
	event, err := (Publisher{DataRoot: root}).Publish(t.Context(), input)
	if err == nil || err.Error() != "authenticated channel requires HTTPS" || event.DeliveryStatus != "failed" {
		t.Fatalf("plaintext credential exposure: %+v, %v", event, err)
	}
}

func TestDeliveryCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			root := t.TempDir()
			started := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			saveProfile(t, root, "slow", Channel{Kind: "webhook", URL: server.URL, AllowPrivate: true})
			input := testInput()
			input.ChannelID = "slow"
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
			}
			defer cancel()
			if !deadline {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			event, err := (Publisher{DataRoot: root}).Publish(ctx, input)
			if err == nil || event.DeliveryStatus != "failed" || !errors.Is(err, ctx.Err()) {
				t.Fatalf("interrupted delivery: %+v, %v", event, err)
			}
			if reloadEvent(t, root, event.EventID).DeliveryStatus != "failed" {
				t.Fatal("interruption not persisted")
			}
		})
	}
}

func TestInputBounds(t *testing.T) {
	cases := map[string]func(*Input){
		"url-channel":   func(i *Input) { i.ChannelID = "https://127.0.0.1" },
		"path-channel":  func(i *Input) { i.ChannelID = "../escape" },
		"long-channel":  func(i *Input) { i.ChannelID = strings.Repeat("x", 65) },
		"severity":      func(i *Input) { i.Severity = "fatal" },
		"empty-title":   func(i *Input) { i.Title = " " },
		"long-title":    func(i *Input) { i.Title = strings.Repeat("x", 257) },
		"empty-summary": func(i *Input) { i.Summary = "" },
		"long-summary":  func(i *Input) { i.Summary = strings.Repeat("x", 8193) },
		"many-refs":     func(i *Input) { i.EvidenceRefs = make([]string, 33) },
		"empty-ref":     func(i *Input) { i.EvidenceRefs = []string{" "} },
		"long-ref":      func(i *Input) { i.EvidenceRefs = []string{strings.Repeat("x", 513)} },
		"encoded-bound": func(i *Input) { i.Summary = strings.Repeat("\x00", 8192) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			input := testInput()
			mutate(&input)
			event, err := (Publisher{DataRoot: root}).Publish(t.Context(), input)
			if err == nil || event.EventID != "" {
				t.Fatalf("accepted invalid input: %+v, %v", event, err)
			}
			if _, err := os.Stat(filepath.Join(root, "notifications")); !os.IsNotExist(err) {
				t.Fatal("invalid input must not create records")
			}
		})
	}
	input := testInput()
	input.Title = strings.Repeat("x", 256)
	input.Summary = strings.Repeat("x", 8192)
	input.EvidenceRefs = make([]string, 32)
	for i := range input.EvidenceRefs {
		input.EvidenceRefs[i] = strings.Repeat("x", 512)
	}
	if _, err := (Publisher{DataRoot: t.TempDir()}).Publish(t.Context(), input); err != nil {
		t.Fatalf("valid upper field bounds: %v", err)
	}
}

func TestProfileAndCredentialBounds(t *testing.T) {
	for _, secret := range []bool{false, true} {
		root := t.TempDir()
		if secret {
			path := filepath.Join(root, "secrets", "notifications", "token")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.Repeat("s", 4097)), 0o600); err != nil {
				t.Fatal(err)
			}
			saveProfile(t, root, "test", Channel{Kind: "webhook", URL: "http://127.0.0.1", AllowPrivate: true, CredentialRef: "secret://notifications/token"})
		} else {
			saveProfile(t, root, "test", Channel{Kind: "webhook", URL: strings.Repeat("x", 16385)})
		}
		input := testInput()
		input.ChannelID = "test"
		event, err := (Publisher{DataRoot: root}).Publish(t.Context(), input)
		if err == nil || event.DeliveryStatus != "failed" {
			t.Fatalf("file bounds bypass: %+v, %v", event, err)
		}
	}
}

func TestPersistenceFailurePreventsSend(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	root := t.TempDir()
	saveProfile(t, root, "test", Channel{Kind: "webhook", URL: server.URL, AllowPrivate: true})
	if err := os.WriteFile(filepath.Join(root, "notifications", "outbox"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := testInput()
	input.ChannelID = "test"
	event, err := (Publisher{DataRoot: root}).Publish(t.Context(), input)
	if err == nil || event.EventID != "" || calls.Load() != 0 {
		t.Fatalf("sent without durable record: %+v, %v", event, err)
	}
}

func TestRelativeDataRootAndOutboxIDBoundary(t *testing.T) {
	runtimeHome := t.TempDir()
	t.Chdir(runtimeHome)
	publisher := Publisher{DataRoot: "saved-data"}
	event, err := publisher.Publish(t.Context(), testInput())
	if err != nil || reloadEvent(t, filepath.Join(runtimeHome, "saved-data"), event.EventID).DeliveryStatus != "stored" {
		t.Fatalf("relative root persistence: %+v, %v", event, err)
	}
	for _, id := range []string{"../outside", "../../outside", "C:outside", "", "0123456789abcdef0123456789abcdef/child"} {
		if err := publisher.save(Event{EventID: id}); err == nil {
			t.Fatalf("accepted event ID path %q", id)
		}
	}
	if _, err := os.Stat(filepath.Join(runtimeHome, "notifications")); !os.IsNotExist(err) {
		t.Fatal("relative root wrote into the runtime directory instead of saved-data")
	}
}

func TestNotificationPathsRejectSymbolicLinks(t *testing.T) {
	for _, family := range []string{"outbox", "channels", "secrets"} {
		t.Run(family, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			link := filepath.Join(root, "notifications", family)
			if family == "secrets" {
				link = filepath.Join(root, "secrets")
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				if runtime.GOOS != "windows" {
					t.Skipf("symbolic links unavailable: %v", err)
				}
				if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
					t.Fatalf("create test junction: %v\n%s", err, output)
				}
			}
			if family == "channels" {
				if _, err := (Publisher{DataRoot: root}).readBounded(filepath.Join("notifications", family, "test.json"), 128); err == nil || !strings.Contains(err.Error(), "symbolic link") {
					t.Fatalf("channel profile boundary did not reject link before reading: %v", err)
				}
			}
			input := testInput()
			if family != "outbox" {
				input.ChannelID = "test"
			}
			if family == "secrets" {
				if err := os.MkdirAll(filepath.Join(outside, "notifications"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(outside, "notifications", "token"), []byte("outside-channel-token"), 0o600); err != nil {
					t.Fatal(err)
				}
				saveProfile(t, root, "test", Channel{Kind: "webhook", URL: "http://127.0.0.1", AllowPrivate: true, CredentialRef: "secret://notifications/token"})
				if _, err := (Publisher{DataRoot: root}).readBounded(filepath.Join("secrets", "notifications", "token"), 128); err == nil || !strings.Contains(err.Error(), "symbolic link") {
					t.Fatalf("credential boundary did not reject link before reading: %v", err)
				}
			}
			event, err := (Publisher{DataRoot: root}).Publish(t.Context(), input)
			if err == nil || strings.Contains(err.Error(), "outside-channel-token") || strings.Contains(event.Error, "outside-channel-token") {
				t.Fatalf("path boundary failed: %+v, %v", event, err)
			}
			if family == "outbox" {
				entries, err := os.ReadDir(outside)
				if err != nil || len(entries) != 0 || event.EventID != "" {
					t.Fatalf("outbox escaped data root: %v, %v", entries, err)
				}
			} else if reloadEvent(t, root, event.EventID).DeliveryStatus != "failed" {
				t.Fatal("unsafe read not recorded as failure")
			}
		})
	}
}
