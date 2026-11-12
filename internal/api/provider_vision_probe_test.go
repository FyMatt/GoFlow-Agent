package api

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

func TestProviderVisionProbeDataURLUsesSupportedImageSize(t *testing.T) {
	dataURL, err := providerVisionProbeDataURL()
	if err != nil {
		t.Fatalf("providerVisionProbeDataURL: %v", err)
	}

	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		t.Fatalf("unexpected probe image data URL prefix")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil {
		t.Fatalf("decode probe image: %v", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("decode probe PNG config: %v", err)
	}
	if config.Width != 64 || config.Height != 64 {
		t.Fatalf("probe image must be 64x64, got %dx%d", config.Width, config.Height)
	}
}
