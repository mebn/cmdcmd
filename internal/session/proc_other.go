//go:build !windows

package session

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
