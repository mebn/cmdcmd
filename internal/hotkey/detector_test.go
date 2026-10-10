package hotkey

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestDetector(t *testing.T) {
	ms := func(n int) time.Time { return time.Unix(0, 0).Add(time.Duration(n) * time.Millisecond) }

	tests := []struct {
		name  string
		steps func(d *detector)
		want  int32
	}{
		{"double tap", func(d *detector) {
			d.modDown(ms(0))
			d.modUp(ms(80))
			d.modDown(ms(200))
			d.modUp(ms(280))
		}, 1},
		{"single tap", func(d *detector) {
			d.modDown(ms(0))
			d.modUp(ms(80))
		}, 0},
		{"taps too far apart", func(d *detector) {
			d.modDown(ms(0))
			d.modUp(ms(80))
			d.modDown(ms(700))
			d.modUp(ms(780))
		}, 0},
		{"held too long", func(d *detector) {
			d.modDown(ms(0))
			d.modUp(ms(80))
			d.modDown(ms(200))
			d.modUp(ms(900))
		}, 0},
		{"shortcut in between", func(d *detector) {
			d.modDown(ms(0))
			d.modUp(ms(80))
			d.modDown(ms(200))
			d.other() // Cmd+C
			d.modUp(ms(280))
		}, 0},
		{"key repeat while held", func(d *detector) {
			d.modDown(ms(0))
			d.modDown(ms(30))
			d.modUp(ms(80))
			d.modDown(ms(200))
			d.modUp(ms(280))
		}, 1},
		{"triple tap fires once", func(d *detector) {
			d.modDown(ms(0))
			d.modUp(ms(50))
			d.modDown(ms(150))
			d.modUp(ms(200))
			d.modDown(ms(300))
			d.modUp(ms(350))
		}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fired atomic.Int32
			done := make(chan struct{}, 4)
			d := newDetector(func() { fired.Add(1); done <- struct{}{} })
			tt.steps(d)
			if tt.want > 0 {
				select {
				case <-done:
				case <-time.After(time.Second):
				}
			}
			time.Sleep(10 * time.Millisecond)
			if got := fired.Load(); got != tt.want {
				t.Fatalf("fired %d times, want %d", got, tt.want)
			}
		})
	}
}
