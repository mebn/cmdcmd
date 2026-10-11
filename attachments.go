package main

import (
	"encoding/base64"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// maxPreview is the largest image shown as a thumbnail in the composer.
const maxPreview = 8 << 20

// Attachment is a file to send with the next message.
type Attachment struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Preview string `json:"preview,omitempty"` // a data: URL, for images
}

func attachment(path string) Attachment {
	a := Attachment{Path: path, Name: filepath.Base(path)}
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if !strings.HasPrefix(mediaType, "image/") {
		return a
	}
	if info, err := os.Stat(path); err != nil || info.Size() > maxPreview {
		return a
	}
	if data, err := os.ReadFile(path); err == nil {
		a.Preview = "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return a
}

func attachments(paths []string) []Attachment {
	out := make([]Attachment, 0, len(paths))
	for _, p := range paths {
		out = append(out, attachment(p))
	}
	return out
}

// ChooseAttachments asks for files to attach. It returns none if the dialog
// was cancelled.
func (l *Launcher) ChooseAttachments() ([]Attachment, error) {
	l.mu.Lock()
	start := l.scopeCwd()
	l.dialogOpen = true
	l.mu.Unlock()

	lowerWindow(l.window, true)
	activateApp()
	dialog := l.app.Dialog.OpenFile().
		SetTitle("Attach files").
		SetButtonText("Attach").
		SetDirectory(start).
		CanChooseFiles(true).
		CanChooseDirectories(false)
	if runtime.GOOS != "darwin" {
		// On macOS this would be a sheet hanging off the invisible stage.
		dialog.AttachToWindow(l.window)
	}
	paths, err := dialog.PromptForMultipleSelection()
	lowerWindow(l.window, false)

	l.mu.Lock()
	l.dialogOpen = false
	l.mu.Unlock()
	l.Show()
	if err != nil {
		return nil, err
	}
	return attachments(paths), nil
}

// ClipboardFiles returns the files copied to the clipboard (in Finder), so
// a paste attaches them in place rather than a copy of their contents.
func (l *Launcher) ClipboardFiles() []Attachment {
	return attachments(clipboardFiles())
}

// SaveAttachment writes pasted data (base64) to a temporary file and returns
// it as an attachment, for content that isn't a file on disk, like a
// screenshot.
func (l *Launcher) SaveAttachment(name, data string) (Attachment, error) {
	bytes, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return Attachment{}, err
	}
	// A directory of its own keeps the file's name without clashing.
	dir, err := os.MkdirTemp("", "cmdcmd-paste-")
	if err != nil {
		return Attachment{}, err
	}
	name = filepath.Base(name)
	if name == "." || name == string(filepath.Separator) {
		name = "pasted"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return Attachment{}, err
	}
	return attachment(path), nil
}
