//go:build !windows

package runner

import "os"

// killProcessTree falls back to killing the direct process on non-Windows
// platforms. (The supported tools target Windows; this keeps cross-compilation
// and tests working elsewhere.)
func killProcessTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	return p.Kill()
}
