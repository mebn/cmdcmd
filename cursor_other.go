//go:build !darwin && !windows

package main

func cursorScreenID() string { return "" }
