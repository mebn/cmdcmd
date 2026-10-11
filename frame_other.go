//go:build !darwin && !windows

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func placeWindow(_ *application.App, window *application.WebviewWindow, width, height int, recenter bool) {
	if !recenter {
		return
	}
	window.SetSize(width, height)
	window.Center()
}

// snapWindow centers the window on its screen if it is close to centered.
func snapWindow(window *application.WebviewWindow, threshold int) {
	snapRelative(window, threshold)
}
