//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// requestFolderAccess is a no-op: only macOS gates folders behind prompts.
func requestFolderAccess() {}

func openURL(url string) {}

// activateApp is a no-op: dialogs come up in front on their own.
func activateApp() {}

func deactivateApp() {}

// lowerWindow is a no-op: dialogs are attached to the window, which keeps
// them on top of it.
func lowerWindow(*application.WebviewWindow, bool) {}
