// Package hotkey detects a double tap of a single modifier key (Cmd on macOS,
// Ctrl on Windows) system-wide and calls a handler when it happens.
package hotkey

import (
	"sync"
	"time"
)

const (
	// maxHold is how long the modifier may be held and still count as a tap.
	maxHold = 350 * time.Millisecond
	// maxGap is the longest pause allowed between the two taps.
	maxGap = 400 * time.Millisecond
)

// detector turns a stream of key events into "double tap" triggers. A tap is
// a press and release of the target modifier with nothing else pressed in
// between, so shortcuts like Cmd+C never count.
type detector struct {
	mu      sync.Mutex
	down    bool
	dirty   bool
	pressAt time.Time
	lastTap time.Time
	fire    func()
}

func newDetector(fire func()) *detector {
	return &detector{fire: fire}
}

// modDown records a press of the target modifier.
func (d *detector) modDown(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.down {
		return // key repeat
	}
	d.down = true
	d.dirty = false
	d.pressAt = t
}

// modUp records a release of the target modifier.
func (d *detector) modUp(t time.Time) {
	d.mu.Lock()
	if !d.down {
		d.mu.Unlock()
		return
	}
	d.down = false
	isTap := !d.dirty && t.Sub(d.pressAt) <= maxHold
	fire := false
	switch {
	case !isTap:
		d.lastTap = time.Time{}
	case !d.lastTap.IsZero() && t.Sub(d.lastTap) <= maxGap:
		d.lastTap = time.Time{}
		fire = true
	default:
		d.lastTap = t
	}
	d.mu.Unlock()

	if fire {
		// Never run the handler on the hook thread: OS hooks must return fast.
		go d.fire()
	}
}

// other records any other key or modifier, which cancels a pending tap.
func (d *detector) other() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dirty = true
	d.lastTap = time.Time{}
}
