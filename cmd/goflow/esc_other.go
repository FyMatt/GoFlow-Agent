//go:build !windows

package main

import (
	"context"
	"io"
)

func startPlatformDoubleEscCancelWatcher(context.Context, io.Reader, io.Writer, context.CancelFunc) func() bool {
	return func() bool { return false }
}
