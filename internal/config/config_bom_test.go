package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Windows editors and PowerShell's Set-Content -Encoding UTF8 write a UTF-8
// BOM; a config saved that way must still load (the server used to die at
// boot with a baffling "invalid character 'ï'" error).
func TestLoadToleratesUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := []byte{0xEF, 0xBB, 0xBF}
	raw = append(raw, []byte(`{"addr": "127.0.0.1:19999", "auth_token": "tok"}`)...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("BOM-prefixed config must load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:19999" {
		t.Errorf("addr = %q", cfg.Addr)
	}
	if cfg.AuthToken != "tok" {
		t.Errorf("auth_token = %q", cfg.AuthToken)
	}
}
