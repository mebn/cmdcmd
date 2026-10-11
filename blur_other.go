//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// setBlur does nothing: only macOS can blur behind parts of the window.
func setBlur(*application.WebviewWindow, []BlurShape) {}
