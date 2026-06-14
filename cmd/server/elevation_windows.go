//go:build windows

package main

import "golang.org/x/sys/windows"

// isElevated reports whether the current process runs with an elevated
// (Administrator) token. Tools like BetterGI need this to simulate input into
// a game that itself runs elevated; without it they exit early (e.g. exit 553).
func isElevated() (bool, bool) {
	var t windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &t); err != nil {
		return false, false // unknown
	}
	defer t.Close()
	return t.IsElevated(), true
}
