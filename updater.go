package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Releases come from this GitHub repository; the release workflow publishes
// a build for each platform on every push to main.
const releaseRepo = "mebn/cmdcmd"

// releaseAsset is the file name of this platform's build in a release.
func releaseAsset() string {
	if runtime.GOOS == "windows" {
		return "cmdcmd-windows-amd64.exe"
	}
	return "cmdcmd-macos-universal.zip"
}

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func latestRelease(ctx context.Context) (release, error) {
	var r release
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+releaseRepo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cmdcmd/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return r, errors.New("no releases published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return r, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	return r, json.NewDecoder(resp.Body).Decode(&r)
}

// isNewer reports whether release tag latest is newer than current. A dev
// build counts as older than any release.
func isNewer(latest, current string) bool {
	if current == "dev" || current == "" {
		return true
	}
	parse := func(v string) []int {
		var out []int
		for _, p := range strings.Split(strings.TrimPrefix(v, "v"), ".") {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		return out
	}
	a, b := parse(latest), parse(current)
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// CheckForUpdates looks for a newer release on GitHub and, if the user
// agrees, downloads it, replaces the running app and relaunches.
func (l *Launcher) CheckForUpdates() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rel, err := latestRelease(ctx)
		if err != nil {
			l.app.Dialog.Error().SetTitle("Couldn't check for updates").SetMessage(err.Error()).Show()
			return
		}
		if !isNewer(rel.Tag, version) {
			l.app.Dialog.Info().SetTitle("cmdcmd is up to date").
				SetMessage("You have the latest version, " + version + ".").Show()
			return
		}
		var url string
		for _, a := range rel.Assets {
			if a.Name == releaseAsset() {
				url = a.URL
			}
		}
		if url == "" {
			l.app.Dialog.Error().SetTitle("Couldn't update").
				SetMessage(rel.Tag + " has no build for this platform (" + releaseAsset() + ").").Show()
			return
		}

		dialog := l.app.Dialog.Question().SetTitle("Update available").
			SetMessage(fmt.Sprintf("cmdcmd %s is available (you have %s). Update and restart now?", rel.Tag, version))
		update := dialog.AddButton("Update")
		update.OnClick(func() { go l.installUpdate(url, rel.Tag) })
		later := dialog.AddButton("Later")
		dialog.SetDefaultButton(update).SetCancelButton(later).Show()
	}()
}

func (l *Launcher) installUpdate(url, tag string) {
	fail := func(err error) {
		l.app.Dialog.Error().SetTitle("Update failed").SetMessage(err.Error()).Show()
	}
	file, err := download(url)
	if err != nil {
		fail(err)
		return
	}
	defer os.Remove(file)
	if err := install(file); err != nil {
		fail(err)
		return
	}
	if err := relaunch(); err != nil {
		fail(fmt.Errorf("installed %s, but couldn't restart: %w", tag, err))
		return
	}
	l.app.Quit()
}

// download saves url to a temporary file and returns its path.
func download(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "cmdcmd/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.CreateTemp("", "cmdcmd-update-*"+filepath.Ext(url))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
