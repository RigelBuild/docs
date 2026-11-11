// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

func IsGatherable(sourcePath string) bool {
	normalized := strings.TrimPrefix(strings.ReplaceAll(sourcePath, "\\", "/"), "./")
	if !strings.EqualFold(filepath.Ext(normalized), ".md") || strings.Contains(normalized, "\\") {
		return false
	}
	parts := strings.Split(normalized, "/")
	if parts[len(parts)-1] == "" {
		return false
	}
	for _, part := range parts {
		if part == "node_modules" || part == "dist" || part == "outputs" {
			return false
		}
		if (strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_")) && normalized != ".github/PULL_REQUEST_TEMPLATE.md" {
			return false
		}
	}
	if strings.HasPrefix(normalized, "docs/designs/") || strings.EqualFold(parts[len(parts)-1], "CHANGELOG.md") {
		return false
	}
	return true
}

func CollectPages(source FetchedSource, config PublishConfig) ([]Page, error) {
	files, err := sourceMarkdownFiles(source)
	if err != nil {
		return nil, err
	}
	eligible, err := eligibleSourceFiles(files, config.Exclude)
	if err != nil {
		return nil, err
	}
	if config.Packages != nil {
		if err := validatePackagePages(eligible, config); err != nil {
			return nil, err
		}
	}
	return makePages(source, config, eligible)
}

func sourceMarkdownFiles(source FetchedSource) ([]string, error) {
	var files []string
	err := filepath.WalkDir(source.Root, func(fullPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(source.Root, fullPath)
		if err != nil {
			return fmt.Errorf("relative source path: %w", err)
		}
		path := filepath.ToSlash(rel)
		if IsGatherable(path) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk source %s: %w", source.Repo, err)
	}
	sort.Strings(files)
	return files, nil
}

func eligibleSourceFiles(files, excludes []string) ([]string, error) {
	eligible := make([]string, 0, len(files))
	for _, file := range files {
		excluded, err := MatchesAny(file, excludes)
		if err != nil {
			return nil, err
		}
		if !excluded {
			eligible = append(eligible, file)
		}
	}
	return eligible, nil
}

func validatePackagePages(files []string, config PublishConfig) error {
	for _, file := range files {
		parts := strings.Split(file, "/")
		name := parts[len(parts)-1]
		readme := (name == "README.md" || name == "AGENTS.md") && len(parts) >= 2 && len(parts) <= config.Packages.ReadmeDepth
		allowed := packagePageAllowed(file, config.Packages.Allow)
		if !readme && !allowed && findSection(file, config.Sections) == nil {
			return fmt.Errorf("stray package page: %s", file)
		}
	}
	return nil
}

func packagePageAllowed(file string, rules []string) bool {
	for _, rule := range rules {
		if strings.HasSuffix(rule, "/") && strings.HasPrefix(file, rule) || file == rule {
			return true
		}
	}
	return false
}

func makePages(source FetchedSource, config PublishConfig, files []string) ([]Page, error) {
	pages := make([]Page, 0, len(files))
	for _, file := range files {
		section := findSection(file, config.Sections)
		if len(config.Sections) > 0 && section == nil && config.Packages == nil {
			if source.Config == ConfigRequired {
				return nil, fmt.Errorf("unassigned page: %s", file)
			}
			continue
		}
		page, err := makePage(source, config, file, section)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, nil
}

func makePage(source FetchedSource, config PublishConfig, file string, section *SectionRule) (Page, error) {
	page := Page{Repo: source.Repo, SourcePath: file, Pagefind: true}
	switch {
	case section != nil:
		page.Section = section.ID
		if section.Pagefind != nil {
			page.Pagefind = *section.Pagefind
		}
		prefix := ""
		if section.Prefix != nil {
			prefix = *section.Prefix
		}
		var relative string
		if containsString(section.Files, file) {
			relative = strings.ReplaceAll(strings.TrimPrefix(file, "."), "/", "-")
		} else {
			relative = strings.TrimPrefix(strings.TrimPrefix(file, prefix), "/")
		}
		page.Route = strings.TrimSuffix(section.Route, "/") + routeSlug(relative)
	case config.Packages != nil:
		page.Route = packageRoute(file, config.Packages)
	default:
		mount := strings.Trim(source.Mount, "/")
		if mount != "" {
			mount += "/"
		}
		page.Route = "/" + source.Repo + routeSlug(mount+file)
	}
	return page, nil
}

func packageRoute(file string, config *PackageRules) string {
	parts := strings.Split(file, "/")
	first := parts[0]
	nested := len(parts) > 2 && containsString(config.Nested, first)
	id := first
	if nested {
		id += "-" + parts[1]
	}
	start := 1
	if nested {
		start = 2
	}
	rest := strings.Join(parts[start:], "/")
	content := strings.Trim(config.Route, "/") + "/" + id
	if rest != "" {
		content += "/" + rest
	}
	return routeSlug(content)
}

func findSection(file string, sections []SectionRule) *SectionRule {
	for index := range sections {
		section := &sections[index]
		if containsString(section.Files, file) {
			return section
		}
		if section.Prefix != nil {
			prefix := *section.Prefix
			if prefix == "" || file == prefix || strings.HasPrefix(file, strings.TrimSuffix(prefix, "/")+"/") {
				return section
			}
		}
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
