package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Streaming requests use the configured timeout for inactivity, including the
// initial response wait. A caller deadline still bounds the whole operation.
func doStreamingRequest(client *http.Client, request *http.Request) (*http.Response, error) {
	if client.Timeout <= 0 {
		return client.Do(request)
	}
	ctx, cancel := context.WithCancelCause(request.Context())
	idleError := fmt.Errorf("stream inactivity timeout after %s: %w", client.Timeout, context.DeadlineExceeded)
	timer := time.AfterFunc(client.Timeout, func() { cancel(idleError) })
	streamClient := *client
	streamClient.Timeout = 0
	response, err := streamClient.Do(request.WithContext(ctx))
	if err != nil {
		timer.Stop()
		if context.Cause(ctx) == idleError {
			err = idleError
		}
		cancel(nil)
		return nil, err
	}
	response.Body = &streamIdleBody{
		ReadCloser: response.Body, ctx: ctx, cancel: cancel, timer: timer,
		timeout: client.Timeout, idleError: idleError,
	}
	return response, nil
}

type streamIdleBody struct {
	io.ReadCloser
	ctx       context.Context
	cancel    context.CancelCauseFunc
	timer     *time.Timer
	timeout   time.Duration
	idleError error
	mu        sync.Mutex
	closed    bool
}

func (body *streamIdleBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	if err != nil && context.Cause(body.ctx) == body.idleError {
		err = body.idleError
	}
	return n, err
}

func recordStreamingProgress(reader io.ReadCloser) {
	if body, ok := reader.(*streamIdleBody); ok {
		body.mu.Lock()
		if !body.closed && body.ctx.Err() == nil {
			body.timer.Reset(body.timeout)
		}
		body.mu.Unlock()
	}
}

func (body *streamIdleBody) Close() error {
	body.mu.Lock()
	if body.closed {
		body.mu.Unlock()
		return nil
	}
	body.closed = true
	body.timer.Stop()
	body.cancel(nil)
	body.mu.Unlock()
	return body.ReadCloser.Close()
}
