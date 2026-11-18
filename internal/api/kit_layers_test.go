package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"gopkg.in/yaml.v3"
)

func writeLayeredKit(t *testing.T, path string, doc kitResourceDocument) []byte {
	t.Helper()
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

func layeredKitPin(t *testing.T, pins []session.ResourcePin) session.ResourcePin {
	t.Helper()
	for _, pin := range pins {
		if pin.Kind == "kit" {
			return pin
		}
	}
	t.Fatalf("missing selected Kit pin: %#v", pins)
	return session.ResourcePin{}
}

func TestKitRoutingReadsRuntimeKitsWithSeparateDataRoot(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "legacy"}[legacy], func(t *testing.T) {
			server, calls, _ := newKitRoutingHTTPRuntime(t, t.TempDir())
			doc := routingTestKit()
			bundledPath := filepath.Join(server.runtime.RuntimeHome(), "kits", doc.Name, "kit.yaml")
			if legacy {
				bundledPath = filepath.Join(server.runtime.RuntimeHome(), "kits", doc.Name+".yaml")
			}
			original := writeLayeredKit(t, bundledPath, doc)
			loaded, ok := server.kitResourceByName(doc.Name)
			if !ok || !sameAPIPath(loaded.Path, bundledPath) {
				t.Fatalf("bundled Kit invisible with separate DataRoot: %#v, %v", loaded, ok)
			}
			list := server.kitResourceList()
			if len(list) != 1 || !sameAPIPath(list[0].Path, bundledPath) {
				t.Fatalf("bundled list: %#v", list)
			}
			request := map[string]any{"type": "auto", "input": "lichen specimen"}
			response := kitRoutingRequest(t, server, http.MethodPost, "/api/runs/preflight", request)
			var preflight TaskPreflightResult
			if err := json.Unmarshal(response.Body.Bytes(), &preflight); err != nil {
				t.Fatal(err)
			}
			if !preflight.Valid || preflight.Target != "lichen-curator" || preflight.Domain != doc.Name {
				t.Fatalf("bundled route used compatibility fallback: %s", response.Body.String())
			}
			bundledPin := layeredKitPin(t, preflight.ResourcePins)
			response = kitRoutingRequest(t, server, http.MethodPost, "/api/runs", request)
			if response.Code != http.StatusOK || calls.Load() == 0 {
				t.Fatalf("bundled run: %d %s", response.Code, response.Body.String())
			}
			runs := server.runtime.SessionSnapshot().AgentRuns
			if len(runs) != 1 || runs[0].AgentID != "lichen-curator" || !reflect.DeepEqual(runs[0].ResourcePins, preflight.ResourcePins) {
				t.Fatalf("bundled pins/execution: %#v", runs)
			}
			// An identical user copy has a different origin; no absolute paths are needed in pin identity.
			installRoutingTestKit(t, server, doc)
			writePath, err := server.kitResourcePath(doc.Name)
			if err != nil {
				t.Fatal(err)
			}
			loaded, ok = server.kitResourceByName(doc.Name)
			if !ok || !sameAPIPath(loaded.Path, writePath) || !referenceWithinBase(server.runtime.DataRoot(), loaded.Path) {
				t.Fatalf("save target/override precedence: %#v", loaded)
			}
			result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
			if !result.Valid || layeredKitPin(t, result.ResourcePins).Version == bundledPin.Version {
				t.Fatalf("origin was not pinned: %#v", result)
			}
			doc.Agents, doc.Routing.EntryAgent = []string{"chat"}, "chat"
			installRoutingTestKit(t, server, doc)
			result = server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
			if !result.Valid || result.Target != "chat" || layeredKitPin(t, result.ResourcePins).Version == bundledPin.Version {
				t.Fatalf("user override not selected/pinned: %#v", result)
			}
			list = server.kitResourceList()
			if len(list) != 1 || !sameAPIPath(list[0].Path, writePath) {
				t.Fatalf("duplicate/incorrect layered list: %#v", list)
			}
			current, err := os.ReadFile(bundledPath)
			if err != nil || !reflect.DeepEqual(current, original) {
				t.Fatal("saving an override modified the bundled Kit")
			}
			stored, _ := server.runtime.AgentRun(runs[0].ID)
			if !reflect.DeepEqual(stored.ResourcePins, runs[0].ResourcePins) {
				t.Fatal("override rewrote historical pins")
			}
		})
	}
}

func TestKitLayersTombstonesHideBundledAndOverride(t *testing.T) {
	for _, withOverride := range []bool{false, true} {
		t.Run(map[bool]string{false: "bundled", true: "override"}[withOverride], func(t *testing.T) {
			server, _, _ := newKitRoutingHTTPRuntime(t, t.TempDir())
			doc := routingTestKit()
			bundledPath := filepath.Join(server.runtime.RuntimeHome(), "kits", doc.Name, "kit.yaml")
			original := writeLayeredKit(t, bundledPath, doc)
			if withOverride {
				installRoutingTestKit(t, server, doc)
			}
			response := kitRoutingRequest(t, server, http.MethodDelete, "/api/resources/kits/"+doc.Name, nil)
			if response.Code != http.StatusNoContent {
				t.Fatalf("layered delete: %d %s", response.Code, response.Body.String())
			}
			hidden, err := server.runtime.IsResourceTombstoned("kits", doc.Name)
			if err != nil || !hidden {
				t.Fatalf("missing Kit tombstone: %v %v", hidden, err)
			}
			if _, ok := server.kitResourceByName(doc.Name); ok || len(server.kitResourceList()) != 0 {
				t.Fatal("deleted bundled Kit reappeared through a lower layer")
			}
			current, err := os.ReadFile(bundledPath)
			if err != nil || !reflect.DeepEqual(current, original) {
				t.Fatal("delete changed immutable bundled Kit")
			}
			// Visibility persists across Server reloads, and explicit save restores it.
			server = NewServer(server.runtime)
			if _, ok := server.kitResourceByName(doc.Name); ok {
				t.Fatal("reload ignored tombstone")
			}
			installRoutingTestKit(t, server, doc)
			hidden, err = server.runtime.IsResourceTombstoned("kits", doc.Name)
			if err != nil || hidden {
				t.Fatalf("save did not restore Kit: %v %v", hidden, err)
			}
			result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
			if !result.Valid || result.Target != "lichen-curator" {
				t.Fatalf("restored Kit not routable: %#v", result)
			}
		})
	}
}

func TestKitLayersReadResourceDirectoryOverrides(t *testing.T) {
	server, _, _ := newKitRoutingHTTPRuntime(t, t.TempDir())
	doc := routingTestKit()
	writeLayeredKit(t, filepath.Join(server.runtime.RuntimeHome(), "kits", doc.Name, "kit.yaml"), doc)
	doc.Agents, doc.Routing.EntryAgent = []string{"chat"}, "chat"
	resourcePath := filepath.Join(server.runtime.DataRoot(), "resources", "kits", doc.Name, "kit.yaml")
	writeLayeredKit(t, resourcePath, doc)
	result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
	if !result.Valid || result.Target != "chat" {
		t.Fatalf("resource directory override ignored: %#v", result)
	}
	loaded, _ := server.kitResourceByName(doc.Name)
	if !sameAPIPath(loaded.Path, resourcePath) {
		t.Fatalf("incorrect selected origin: %q", loaded.Path)
	}
	if err := server.runtime.SetResourceTombstone("kits", doc.Name, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.kitResourceByName(doc.Name); ok {
		t.Fatal("resource directory override bypassed tombstone")
	}
}

func TestKitLayerPinsArePortableAcrossInstallationPaths(t *testing.T) {
	var expected session.ResourcePin
	for i := 0; i < 2; i++ {
		server, _, _ := newKitRoutingHTTPRuntime(t, t.TempDir())
		doc := routingTestKit()
		writeLayeredKit(t, filepath.Join(server.runtime.RuntimeHome(), "kits", doc.Name, "kit.yaml"), doc)
		result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
		if !result.Valid {
			t.Fatalf("bundled preflight: %#v", result)
		}
		pin := layeredKitPin(t, result.ResourcePins)
		if i == 0 {
			expected = pin
		} else if pin != expected {
			t.Fatalf("machine paths leaked into Kit pin identity: %#v != %#v", pin, expected)
		}
	}
}

func TestKitLayersMalformedOverrideDoesNotRevealBundledCopy(t *testing.T) {
	server, _, _ := newKitRoutingHTTPRuntime(t, t.TempDir())
	doc := routingTestKit()
	writeLayeredKit(t, filepath.Join(server.runtime.RuntimeHome(), "kits", doc.Name, "kit.yaml"), doc)
	path, err := server.kitResourcePath(doc.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("routing: [invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.kitResourceByName(doc.Name); ok {
		t.Fatal("malformed override silently exposed the bundled Kit")
	}
}
