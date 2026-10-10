package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// appBundle returns the .app bundle this process runs from.
func appBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // .app/Contents/MacOS/cmdcmd
	if !strings.HasSuffix(bundle, ".app") {
		return "", errors.New("cmdcmd is not running from an app bundle")
	}
	return bundle, nil
}

// install unpacks the downloaded zip and swaps the new cmdcmd.app in for the
// running one. The running process keeps working from the replaced files.
func install(zip string) error {
	bundle, err := appBundle()
	if err != nil {
		return err
	}
	// Unpack next to the bundle so the final rename stays on one volume.
	tmp, err := os.MkdirTemp(filepath.Dir(bundle), ".cmdcmd-update-")
	if err != nil {
		return fmt.Errorf("can't write next to %s: %w", bundle, err)
	}
	defer os.RemoveAll(tmp)
	// ditto keeps the bundle's symlinks, permissions and signature intact.
	if out, err := exec.Command("ditto", "-x", "-k", zip, tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("unzip: %v: %s", err, out)
	}
	fresh := filepath.Join(tmp, "cmdcmd.app")
	if _, err := os.Stat(fresh); err != nil {
		return errors.New("the download doesn't contain cmdcmd.app")
	}

	old := filepath.Join(tmp, "previous.app")
	if err := os.Rename(bundle, old); err != nil {
		return err
	}
	if err := os.Rename(fresh, bundle); err != nil {
		os.Rename(old, bundle)
		return err
	}
	return nil
}

// relaunch opens the app again once this process has quit.
func relaunch() error {
	bundle, err := appBundle()
	if err != nil {
		return err
	}
	return exec.Command("/bin/sh", "-c", `sleep 1; open "$0"`, bundle).Start()
}

func cleanupUpdate() {}
