// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func WriteManifest(distDir string, manifest Manifest) error {
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bundle manifest: %w", err)
	}
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "_bundle.json"), append(content, '\n'), 0o644); err != nil {
		return fmt.Errorf("write bundle manifest: %w", err)
	}
	return nil
}
