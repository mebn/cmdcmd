package main

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

// install replaces the running cmdcmd.exe with the downloaded one. Windows
// can't overwrite a running executable but can rename it, so the old one is
// moved aside and deleted on the next start.
func install(downloaded string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := copyFile(downloaded, exe); err != nil {
		os.Rename(old, exe)
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// relaunch starts cmdcmd again after a short delay, so the single-instance
// lock is free by the time it starts.
func relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/C", `ping -n 3 127.0.0.1 >nul & start "" "`+exe+`"`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd.Start()
}

// cleanupUpdate deletes the executable left behind by the last update.
func cleanupUpdate() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
