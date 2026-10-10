package session

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps Windows from flashing a console window for claude.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
