//go:build !windows

package wsl

import "os/exec"

func defaultDistro() string { return "" }

func hideWindow(*exec.Cmd) {}
