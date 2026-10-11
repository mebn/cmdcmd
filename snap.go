//go:build !darwin

package main

import (
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// snapToCenter centers the window on its screen once it comes to rest within
// threshold pixels of it after being dragged.
func snapToCenter(window *application.WebviewWindow, threshold int) {
	var mu sync.Mutex
	var timer *time.Timer
	window.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(250*time.Millisecond, func() { snapRelative(window, threshold) })
	})
}

// snapRelative moves the window to the center of its screen's work area if
// it is within threshold pixels of it.
func snapRelative(window *application.WebviewWindow, threshold int) {
	screen, err := window.GetScreen()
	if err != nil || screen == nil {
		return
	}
	width, height := window.Size()
	x, y := window.RelativePosition()
	cx, cy := (screen.WorkArea.Width-width)/2, (screen.WorkArea.Height-height)/2
	if (x != cx || y != cy) && abs(x-cx) <= threshold && abs(y-cy) <= threshold {
		window.SetRelativePosition(cx, cy)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
