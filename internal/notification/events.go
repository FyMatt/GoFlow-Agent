// Package notification provides domain-neutral durable events and saved webhook channels.
package notification

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
)

const (
	MaxPayloadBytes = 32 * 1024
	DeliveryTimeout = 10 * time.Second
)

var channelID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var eventID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Input struct {
	ChannelID    string   `json:"channel_id,omitempty"`
	Severity     string   `json:"severity"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type Event struct {
	SchemaVersion int    `json:"schema_version"`
	EventID       string `json:"event_id"`
	Input
	CreatedAt      string `json:"created_at"`
	DeliveryStatus string `json:"delivery_status"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Channel is saved by the user, never supplied through publish_event arguments.
type Channel struct {
	Kind          string `json:"kind"`
	URL           string `json:"url"`
	CredentialRef string `json:"credential_ref,omitempty"`
	AllowPrivate  bool   `json:"allow_private,omitempty"`
}

type Publisher struct {
	DataRoot        string
	NetworkDisabled bool
}

// Publish persists before attempting delivery. It does not retry: pending records
// after process termination have an unknown delivery outcome, not confirmed success.
func (p Publisher) Publish(ctx context.Context, input Input) (Event, error) {
	if input.ChannelID == "" {
		input.ChannelID = "local"
	}
	if err := validateInput(input); err != nil {
		return Event{}, err
	}
	if strings.TrimSpace(p.DataRoot) == "" {
		return Event{}, errors.New("notification data root is required")
	}
	dataRoot, err := filepath.Abs(strings.TrimSpace(p.DataRoot))
	if err != nil {
		return Event{}, errors.New("invalid notification data root")
	}
	p.DataRoot = dataRoot
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Event{}, errors.New("generate event ID failed")
	}
	event := Event{
		SchemaVersion: 1, EventID: hex.EncodeToString(id[:]), Input: input,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), DeliveryStatus: "pending",
	}
	if input.ChannelID == "local" {
		event.DeliveryStatus = "stored"
	}
	if err := p.save(event); err != nil {
		return Event{}, err
	}
	if input.ChannelID == "local" {
		return event, nil
	}
	ctx, cancel := context.WithTimeout(ctx, DeliveryTimeout)
	defer cancel()
	status, err := p.deliver(ctx, event)
	event.HTTPStatus = status
	event.DeliveryStatus = "delivered"
	if err != nil {
		event.DeliveryStatus = "failed"
		event.Error = err.Error()
	}
	if saveErr := p.save(event); saveErr != nil {
		return event, errors.New("could not persist delivery outcome; do not automatically resend")
	}
	return event, err
}

func (p Publisher) save(event Event) error {
	if !eventID.MatchString(event.EventID) {
		return errors.New("invalid event ID")
	}
	path, err := persistence.New(p.DataRoot).PathWithinRoot(filepath.Join("notifications", "outbox", event.EventID+".json"))
	if err != nil {
		return errors.New("notification outbox path is not trusted")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return errors.New("encode event failed")
	}
	if err := persistence.WriteFileAtomic(path, data, 0o600); err != nil {
		return errors.New("persist notification event failed")
	}
	return nil
}

func validateInput(input Input) error {
	if !channelID.MatchString(input.ChannelID) {
		return errors.New("channel_id must be 1-64 ASCII letters, digits, underscores or hyphens, starting with a letter or digit")
	}
	switch input.Severity {
	case "info", "warning", "error", "critical":
	default:
		return errors.New("severity must be info, warning, error or critical")
	}
	if strings.TrimSpace(input.Title) == "" || len(input.Title) > 256 {
		return errors.New("title must contain 1-256 bytes")
	}
	if strings.TrimSpace(input.Summary) == "" || len(input.Summary) > 8192 {
		return errors.New("summary must contain 1-8192 bytes")
	}
	if len(input.EvidenceRefs) > 32 {
		return errors.New("evidence_refs must contain at most 32 references")
	}
	for _, ref := range input.EvidenceRefs {
		if strings.TrimSpace(ref) == "" || len(ref) > 512 {
			return errors.New("each evidence reference must contain 1-512 bytes")
		}
	}
	data, _ := json.Marshal(input)
	if len(data) > MaxPayloadBytes-1024 {
		return errors.New("encoded event payload is too large")
	}
	return nil
}

func (p Publisher) readBounded(relative string, limit int64) ([]byte, error) {
	path, err := persistence.New(p.DataRoot).PathWithinRoot(relative)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("notification input must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("file exceeds size limit")
	}
	return data, err
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
