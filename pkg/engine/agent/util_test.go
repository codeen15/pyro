package agent

import (
	"os"
	"path/filepath"
)

func writeFile(p, s string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(s), 0o644)
}
