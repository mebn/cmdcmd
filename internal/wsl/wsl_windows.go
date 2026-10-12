package wsl

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// defaultDistro reads the default distro's name from where WSL keeps it.
func defaultDistro() string {
	lxss, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Lxss`, registry.READ)
	if err != nil {
		return ""
	}
	defer lxss.Close()
	id, _, err := lxss.GetStringValue("DefaultDistribution")
	if err != nil {
		return ""
	}
	key, err := registry.OpenKey(lxss, id, registry.READ)
	if err != nil {
		return ""
	}
	defer key.Close()
	name, _, _ := key.GetStringValue("DistributionName")
	return name
}

// hideWindow keeps wsl.exe from flashing a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
