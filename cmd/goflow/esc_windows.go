//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	consoleKeyEvent = 0x0001
	vkEscape        = 0x1B
)

var procReadConsoleInputW = syscall.NewLazyDLL("kernel32.dll").NewProc("ReadConsoleInputW")

type consoleInputRecord struct {
	eventType uint16
	_         uint16
	keyEvent  consoleKeyEventRecord
}

type consoleKeyEventRecord struct {
	keyDown         int32
	repeatCount     uint16
	virtualKeyCode  uint16
	virtualScanCode uint16
	unicodeChar     uint16
	controlKeyState uint32
}

func startPlatformDoubleEscCancelWatcher(ctx context.Context, input io.Reader, output io.Writer, cancel context.CancelFunc) func() bool {
	file, ok := input.(*os.File)
	if !ok {
		return func() bool { return false }
	}
	handle := windows.Handle(file.Fd())
	if err := procReadConsoleInputW.Find(); err != nil {
		return func() bool { return false }
	}

	var cancelled atomic.Bool
	stopCh := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		canceller := newDoubleEscCanceller(doubleEscCancelWindow)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				if readEsc, err := drainConsoleInputForEsc(handle); err != nil {
					return
				} else if readEsc {
					switch canceller.press(time.Now()) {
					case doubleEscActionWarn:
						fmt.Fprintln(output, "\n"+formatEscCancelWarning())
					case doubleEscActionCancel:
						cancelled.Store(true)
						fmt.Fprintln(output, "\n"+formatEscCancelling())
						cancel()
						return
					}
				}
			}
		}
	}()

	return func() bool {
		stopOnce.Do(func() { close(stopCh) })
		<-done
		return cancelled.Load()
	}
}

func drainConsoleInputForEsc(handle windows.Handle) (bool, error) {
	readEsc := false
	for {
		var eventCount uint32
		if err := windows.GetNumberOfConsoleInputEvents(handle, &eventCount); err != nil {
			return readEsc, err
		}
		if eventCount == 0 {
			return readEsc, nil
		}
		batchSize := eventCount
		if batchSize > 16 {
			batchSize = 16
		}
		records := make([]consoleInputRecord, batchSize)
		read, err := readConsoleInput(handle, records)
		if err != nil {
			return readEsc, err
		}
		for _, record := range records[:read] {
			if isEscKeyDown(record) {
				readEsc = true
			}
		}
	}
}

func readConsoleInput(handle windows.Handle, records []consoleInputRecord) (uint32, error) {
	if len(records) == 0 {
		return 0, nil
	}
	var read uint32
	ret, _, err := procReadConsoleInputW.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&records[0])),
		uintptr(len(records)),
		uintptr(unsafe.Pointer(&read)),
	)
	if ret == 0 {
		if err != syscall.Errno(0) {
			return read, err
		}
		return read, syscall.EINVAL
	}
	return read, nil
}

func isEscKeyDown(record consoleInputRecord) bool {
	return record.eventType == consoleKeyEvent &&
		record.keyEvent.keyDown != 0 &&
		record.keyEvent.virtualKeyCode == vkEscape
}
