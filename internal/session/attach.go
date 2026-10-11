package session

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

// maxInlineImage is the largest image sent inline; the API takes up to 5 MB
// of base64. Larger ones are named by path, and the agent reads (and
// downsizes) them itself.
const maxInlineImage = 3_750_000

// imageTypes are the image formats the API accepts inline.
var imageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// imageBlock returns an image content block for the file, or nil if it isn't
// an image that can go inline.
func imageBlock(path string) map[string]any {
	mediaType, ok := imageTypes[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil
	}
	if info, err := os.Stat(path); err != nil || info.Size() > maxInlineImage {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       base64.StdEncoding.EncodeToString(data),
		},
	}
}

// withPaths adds the attached files to a message as a list of paths.
func withPaths(text string, files []string) string {
	if len(files) == 0 {
		return text
	}
	var sb strings.Builder
	sb.WriteString(text)
	if text != "" {
		sb.WriteString("\n\n")
	}
	sb.WriteString("Attached files:")
	for _, f := range files {
		sb.WriteString("\n- ")
		sb.WriteString(f)
	}
	return sb.String()
}
