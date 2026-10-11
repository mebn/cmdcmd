package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// State is what cmdcmd remembers between runs that is not a setting: the
// folders in the bar, which one is selected, and pinned or deleted sessions.
// It lives next to the config file in state.json.
type State struct {
	Folders  []Folder `json:"folders"`
	Selected string   `json:"selected"` // folder path; "" means All
	// Pinned and Deleted hold agent session IDs.
	Pinned  []string `json:"pinnedSessions"`
	Deleted []string `json:"deletedSessions"`
	// Removed holds folders taken out of the bar, so ones found from Claude
	// Code's history aren't added back.
	Removed []string `json:"removedFolders"`
}

// Folder is a directory sessions can run in, in the order shown in the bar.
type Folder struct {
	Path   string `json:"path"`
	Pinned bool   `json:"pinned,omitempty"`
}

func statePath() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "state.json"), nil
}

// LoadState reads state.json; a missing file is an empty state.
func LoadState() (State, error) {
	var s State
	path, err := statePath()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}

// Save writes state.json.
func (s State) Save() error {
	path, err := statePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// HasFolder reports whether path is one of the folders.
func (s State) HasFolder(path string) bool {
	return slices.ContainsFunc(s.Folders, func(f Folder) bool { return f.Path == path })
}
