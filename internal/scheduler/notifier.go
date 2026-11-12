package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPNotifier struct {
	client   *http.Client
	resolver *net.Resolver
}

type notificationPrivateContextKey struct{}

func NewHTTPNotifier() *HTTPNotifier {
	notifier := &HTTPNotifier{resolver: net.DefaultResolver}
	notifier.client = &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("notification redirect limit exceeded")
			}
			allowPrivate := false
			if len(via) > 0 {
				allowPrivate, _ = via[0].Context().Value(notificationPrivateContextKey{}).(bool)
			}
			return notifier.validateURL(request.Context(), request.URL, allowPrivate)
		},
	}
	return notifier
}

func (n *HTTPNotifier) Notify(schedule Schedule, job Job) error {
	if n == nil || n.client == nil || n.resolver == nil {
		return fmt.Errorf("notification client is not configured")
	}
	target, err := url.Parse(schedule.Notify.URL)
	if err != nil {
		return fmt.Errorf("parse notification URL: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, notificationPrivateContextKey{}, schedule.Notify.AllowPrivate)
	if err := n.validateURL(ctx, target, schedule.Notify.AllowPrivate); err != nil {
		return err
	}
	payload := map[string]any{
		"event": "goflow.schedule.completed", "schedule_id": schedule.ID, "schedule_name": schedule.Name,
		"job_id": job.ID, "execution_key": job.ExecutionKey, "run_id": job.RunID,
		"status": job.Status, "error": job.Error, "scheduled_for": job.ScheduledFor, "completed_at": job.CompletedAt,
	}
	if schedule.Notify.Kind == "ntfy" {
		payload["topic"] = schedule.Notify.Topic
		payload["title"] = "GoFlow: " + schedule.Name
		payload["message"] = fmt.Sprintf("Scheduled task %s finished with status %s", schedule.Name, job.Status)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", job.ExecutionKey)
	request.Header.Set("User-Agent", "GoFlow-Scheduler/1")
	response, err := n.client.Do(request)
	if err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("notification endpoint returned %s", response.Status)
	}
	return nil
}

func (n *HTTPNotifier) validateURL(ctx context.Context, target *url.URL, allowPrivate bool) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || strings.TrimSpace(target.Hostname()) == "" {
		return fmt.Errorf("notification URL must use http or https with a host")
	}
	if target.User != nil || target.Fragment != "" {
		return fmt.Errorf("notification URL cannot contain credentials or a fragment")
	}
	addresses, err := n.resolver.LookupIPAddr(ctx, target.Hostname())
	if err != nil {
		return fmt.Errorf("resolve notification host: %w", err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("notification host did not resolve to an address")
	}
	if allowPrivate {
		return nil
	}
	for _, address := range addresses {
		if notificationIPPrivate(address.IP) {
			return fmt.Errorf("notification URL resolves to a private or local address; set allow_private only for an intentional local endpoint")
		}
	}
	return nil
}

func notificationIPPrivate(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}
