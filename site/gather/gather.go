// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RigelBuild/docs/site/gather/internal/bundle"
	"github.com/RigelBuild/meissa/go/ledger"
)

const repoSlug = "RigelBuild/docs"

func gather(repoRoot, siteRoot string, fetched []bundle.FetchedSource, logger *slog.Logger) (bundle.Manifest, error) {
	if err := bundle.AssertFetchedSources(siteRoot, fetched); err != nil {
		return bundle.Manifest{}, err
	}
	designs, err := loadDesignSources(filepath.Join(repoRoot, "docs"), repoRoot)
	if err != nil {
		return bundle.Manifest{}, err
	}
	contentRoot := filepath.Join(siteRoot, "src", "content", "docs")
	if err := resetContentRoot(contentRoot); err != nil {
		return bundle.Manifest{}, err
	}
	if err := writeLanding(contentRoot, fetched); err != nil {
		return bundle.Manifest{}, err
	}
	gathered := make(map[string]string, len(designs))
	for sourcePath := range designs {
		gathered[sourcePath] = ""
	}
	decisions, designPages, err := writeDesignPages(contentRoot, designs, gathered)
	if err != nil {
		return bundle.Manifest{}, err
	}
	if err := writeDecisionIndexes(contentRoot, decisions, designs); err != nil {
		return bundle.Manifest{}, err
	}
	fetchedPages, err := loadFetchedPages(fetched)
	if err != nil {
		return bundle.Manifest{}, err
	}
	allPages := append(designPages, fetchedPages...)
	if err := bundle.AssertNoRouteCollisions(allPages); err != nil {
		return bundle.Manifest{}, err
	}
	routes := buildPageRoutes(allPages)
	if err := writeFetchedPages(contentRoot, fetched, fetchedPages, routes, logger); err != nil {
		return bundle.Manifest{}, err
	}
	manifest := makeManifest(fetched, fetchedPages)
	if err := bundle.WriteManifest(filepath.Join(siteRoot, "public"), manifest); err != nil {
		return bundle.Manifest{}, err
	}
	return manifest, nil
}

func loadDesignSources(docsRoot, repoRoot string) (map[string]string, error) {
	files, err := markdownFiles(docsRoot)
	if err != nil {
		return nil, fmt.Errorf("walk docs: %w", err)
	}
	sort.Strings(files)
	sources := make(map[string]string, len(files))
	for _, file := range files {
		rel, err := filepath.Rel(repoRoot, file)
		if err != nil {
			return nil, fmt.Errorf("relative path: %w", err)
		}
		sourcePath := filepath.ToSlash(rel)
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", sourcePath, err)
		}
		sources[sourcePath] = string(content)
	}
	return sources, nil
}

func resetContentRoot(contentRoot string) error {
	if err := os.RemoveAll(contentRoot); err != nil {
		return fmt.Errorf("clear content: %w", err)
	}
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		return fmt.Errorf("create content: %w", err)
	}
	return nil
}

func writeLanding(contentRoot string, fetched []bundle.FetchedSource) error {
	sourceList := make([]string, 0, len(fetched))
	for _, source := range fetched {
		mount := strings.Trim(source.Mount, "/")
		repoName := source.Repo[strings.IndexByte(source.Repo, '/')+1:]
		sourceList = append(sourceList, fmt.Sprintf("- [%s](/%s/)", repoName, mount))
	}
	landing := bundle.Frontmatter("Rigel public design docs", "", "") + "\nDesign records and decisions for Rigel's public repositories.\n\n## Designs\n\nProjects are listed in the sidebar. Their design records and decisions are grouped by repository.\n\n## Code repositories\n\nEach bundled repository has its own sidebar group.\n\n" + strings.Join(sourceList, "\n") + "\n"
	return writePage(filepath.Join(contentRoot, "index.md"), landing)
}

func writeDesignPages(contentRoot string, sources, gathered map[string]string) (map[string][]ledger.DecisionRow, []bundle.Page, error) {
	decisions := make(map[string][]ledger.DecisionRow)
	pages := make([]bundle.Page, 0, len(sources))
	for _, sourcePath := range sortedKeys(sources) {
		source := sources[sourcePath]
		page, err := bundle.Transform(source, sourcePath, gathered)
		if err != nil {
			return nil, nil, err
		}
		destination := filepath.Join(contentRoot, filepath.FromSlash(strings.TrimPrefix(sourcePath, "docs/")))
		if err := writePage(destination, page); err != nil {
			return nil, nil, err
		}
		pages = append(pages, bundle.Page{Repo: repoSlug, SourcePath: sourcePath, Route: bundle.RouteSlug(strings.TrimPrefix(sourcePath, "docs/")), Pagefind: true})
		if match := bundle.DesignDecisionPathMatch(sourcePath); match != nil {
			row, err := ledger.ParseDecisionFile(sourcePath, []byte(source))
			if err != nil {
				return nil, nil, err
			}
			decisions[match[1]] = append(decisions[match[1]], row)
		}
	}
	return decisions, pages, nil
}

func writeDecisionIndexes(contentRoot string, decisions map[string][]ledger.DecisionRow, sources map[string]string) error {
	for _, surface := range sortedKeys(decisions) {
		destination := filepath.Join(contentRoot, "designs", surface, "decisions", "index.md")
		if err := writePage(destination, bundle.BuildDecisionIndex(surface, decisions[surface], sources)); err != nil {
			return err
		}
	}
	return nil
}

func loadFetchedPages(fetched []bundle.FetchedSource) ([]bundle.Page, error) {
	pages := make([]bundle.Page, 0)
	for _, source := range fetched {
		if strings.Trim(source.Mount, "/") == "designs" {
			return nil, fmt.Errorf("source mount may not be designs: %s", source.Repo)
		}
		config, err := bundle.ReadPublishConfig(source.Root, source.Config)
		if err != nil {
			return nil, err
		}
		sourcePages, err := bundle.CollectPages(source, config)
		if err != nil {
			return nil, err
		}

		sourcePages, err = bundle.RemapSourceRoutes(sourcePages, source)
		if err != nil {
			return nil, err
		}
		pages = append(pages, sourcePages...)
	}
	return pages, nil
}

func buildPageRoutes(pages []bundle.Page) map[string]string {
	routes := make(map[string]string, len(pages))
	for _, page := range pages {
		routes[page.Repo+":"+page.SourcePath] = page.Route
	}
	return routes
}

func writeFetchedPages(contentRoot string, sources []bundle.FetchedSource, pages []bundle.Page, routes map[string]string, logger *slog.Logger) error {
	for _, source := range sources {
		for _, page := range pages {
			if page.Repo == source.Repo {
				if err := writeFetchedPage(contentRoot, source, page, routes, logger); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func writeFetchedPage(contentRoot string, source bundle.FetchedSource, page bundle.Page, routes map[string]string, logger *slog.Logger) error {
	markdown, err := os.ReadFile(filepath.Join(source.Root, filepath.FromSlash(page.SourcePath)))
	if err != nil {
		return fmt.Errorf("read %s/%s: %w", source.Repo, page.SourcePath, err)
	}
	label := source.Repo + "/" + page.SourcePath
	body, fields := bundle.StripSourceFrontmatter(string(markdown), label, logger)
	title := bundle.PageTitle(body, fields, page.SourcePath)
	content := bundle.Frontmatter(title, source.Repo, page.SourcePath)
	body = bundle.RewriteLinksWithCallback(bundle.StripFirstH1(body), page, routes, source.Root, func(target string) {
		logger.Warn("gather: broken link " + source.Repo + "/" + page.SourcePath + " -> " + target)
	})
	return writePage(filepath.Join(contentRoot, strings.TrimPrefix(page.Route, "/"), "index.md"), content+body)
}

func makeManifest(sources []bundle.FetchedSource, pages []bundle.Page) bundle.Manifest {
	manifest := bundle.Manifest{}
	manifest.DocsRepo.Repo = repoSlug
	manifest.DocsRepo.SHA = os.Getenv("GITHUB_SHA")
	if manifest.DocsRepo.SHA == "" {
		manifest.DocsRepo.SHA = "local"
	}
	manifest.Sources = make([]bundle.ManifestSource, 0, len(sources))
	for _, source := range sources {
		pageCount := 0
		for _, page := range pages {
			if page.Repo == source.Repo {
				pageCount++
			}
		}
		manifest.Sources = append(manifest.Sources, bundle.ManifestSource{Repo: source.Repo, Ref: "main", SHA: source.SHA, Pages: pageCount})
	}
	return manifest
}

func writePage(file, content string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", file, err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	return nil
}

func markdownFiles(directory string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(directory, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		files = append(files, file)
		return nil
	})
	return files, err
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
