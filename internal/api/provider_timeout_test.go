package api

import (
	"testing"
	"time"
)

func TestProviderTestTimeoutUsesConfiguredValueUpToFiveMinutes(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{name: "default", input: 0, expected: 5 * time.Minute},
		{name: "configured shorter", input: 30 * time.Second, expected: 30 * time.Second},
		{name: "configured longer", input: 10 * time.Minute, expected: 5 * time.Minute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := providerTestTimeout(test.input); got != test.expected {
				t.Fatalf("providerTestTimeout(%s) = %s, want %s", test.input, got, test.expected)
			}
		})
	}
}
