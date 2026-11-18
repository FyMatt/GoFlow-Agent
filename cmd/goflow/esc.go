package main

import (
	"context"
	"fmt"
	"io"
	"time"
)

const doubleEscCancelWindow = 2 * time.Second

type doubleEscAction int

const (
	doubleEscActionWarn doubleEscAction = iota + 1
	doubleEscActionCancel
)

type doubleEscCanceller struct {
	window  time.Duration
	lastEsc time.Time
}

func newDoubleEscCanceller(window time.Duration) *doubleEscCanceller {
	if window <= 0 {
		window = doubleEscCancelWindow
	}
	return &doubleEscCanceller{window: window}
}

func (c *doubleEscCanceller) press(now time.Time) doubleEscAction {
	if c == nil {
		return doubleEscActionWarn
	}
	if !c.lastEsc.IsZero() && now.Sub(c.lastEsc) <= c.window {
		c.lastEsc = time.Time{}
		return doubleEscActionCancel
	}
	c.lastEsc = now
	return doubleEscActionWarn
}

func startDoubleEscCancelWatcher(ctx context.Context, input io.Reader, output io.Writer, cancel context.CancelFunc) func() bool {
	if cancel == nil || !terminalInputSupported(input, output) {
		return func() bool { return false }
	}
	return startPlatformDoubleEscCancelWatcher(ctx, input, output, cancel)
}

func formatTaskCancelled() string {
	return fmt.Sprintf("%s Task cancelled.", styleStatus("[cancel]", "denied"))
}

func formatEscCancelWarning() string {
	return fmt.Sprintf("%s Press Esc again within 2s to cancel this task.", styleStatus("[cancel]", "approval"))
}

func formatEscCancelling() string {
	return fmt.Sprintf("%s Cancelling current task...", styleStatus("[cancel]", "denied"))
}
