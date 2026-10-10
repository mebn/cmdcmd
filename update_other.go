//go:build !darwin && !windows

package main

import "errors"

func install(string) error { return errors.New("updating is not supported on this platform") }
func relaunch() error      { return errors.New("updating is not supported on this platform") }
func cleanupUpdate()       {}
