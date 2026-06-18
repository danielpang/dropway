// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package auth

import (
	"os/exec"
	"runtime"
)

// OpenBrowser opens url in the user's default browser. It returns an error if the
// platform launcher can't be started; callers should print the URL as a fallback
// so the user can open it manually (e.g. headless or SSH sessions).
func OpenBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default: // linux, *bsd
		cmd, args = "xdg-open", []string{url}
	}
	return exec.Command(cmd, args...).Start()
}
