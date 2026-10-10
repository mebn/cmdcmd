//go:build !darwin && !windows

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func placeWindow(_ *application.App, window *application.WebviewWindow, width, height int) {
	window.SetSize(width, height)
	window.Center()
}
