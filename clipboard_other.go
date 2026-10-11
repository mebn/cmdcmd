//go:build !darwin

package main

// clipboardFiles returns nothing: pasted files come through the web view,
// which hands over their contents.
func clipboardFiles() []string { return nil }
