//go:build !darwin && !windows

package hotkey

import "errors"

// ErrPermission is unused on unsupported platforms.
var ErrPermission = errors.New("permission denied")

// SettingsURL is empty on unsupported platforms.
const SettingsURL = ""

// Start reports that no global hotkey is available on this platform.
func Start(mod Modifier, fire func(), ready func()) error {
	return errors.New("global hotkey is not supported on this platform")
}
