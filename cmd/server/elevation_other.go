//go:build !windows

package main

// isElevated is a no-op on non-Windows platforms (the supported game tools run
// on Windows). Second return is false = "elevation status unknown", so callers
// skip the Windows-only admin warning.
func isElevated() (bool, bool) { return false, false }
