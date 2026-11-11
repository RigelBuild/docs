// Command gather copies checked-in design docs into the Starlight content tree.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/RigelBuild/docs/site/gather/internal/bundle"
)

func main() {
	os.Exit(run(os.Stdout, slog.New(slog.NewTextHandler(os.Stderr, nil))))
}

// run gathers the docs under the repository root. The command runs from
// site/gather, so the site root is its parent and the repo root is two up.
func run(out io.Writer, logger *slog.Logger) int {
	moduleDir, err := os.Getwd()
	if err != nil {
		logger.Error("resolve working directory", "err", err)
		return 1
	}
	siteRoot := filepath.Dir(moduleDir)
	repoRoot := filepath.Dir(siteRoot)
	sources, err := bundle.ReadFetched(filepath.Join(siteRoot, ".bundle", "fetched.json"))
	if err != nil {
		logger.Error("read fetched sources", "err", err)
		return 1
	}
	manifest, err := gather(repoRoot, siteRoot, sources, logger)
	if err != nil {
		logger.Error("gather docs", "err", err)
		return 1
	}
	if _, err := fmt.Fprintf(out, "gather: wrote %d bundled sources\n", len(manifest.Sources)); err != nil {
		logger.Error("write status", "err", err)
		return 1
	}
	return 0
}
