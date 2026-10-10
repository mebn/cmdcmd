//go:build !darwin

package main

// requestFolderAccess is a no-op: only macOS gates folders behind prompts.
func requestFolderAccess() {}

func openURL(url string) {}
