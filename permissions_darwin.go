package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// requestFolderAccess opens each privacy-protected folder once so macOS asks
// for access at launch instead of in the middle of a task. claude runs as a
// child of cmdcmd, so macOS attributes its file access to cmdcmd and these
// grants cover the tools your skills run. Each prompt appears only until it
// is answered once.
func requestFolderAccess() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, dir := range []string{"Desktop", "Documents", "Downloads"} {
		os.ReadDir(filepath.Join(home, dir))
	}
}

func openURL(url string) {
	exec.Command("open", url).Start()
}
