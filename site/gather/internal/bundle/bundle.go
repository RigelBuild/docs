// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"
)

func ReadFetched(file string) ([]FetchedSource, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []FetchedSource{}, nil
		}
		return nil, fmt.Errorf("read fetched source file: %w", err)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return nil, fmt.Errorf("invalid fetched source file: %s", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("fetched source file must contain an array")
	}
	sources := make([]FetchedSource, 0, len(raw))
	for _, item := range raw {
		source, err := parseFetchedSource(item)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func parseFetchedSource(item json.RawMessage) (FetchedSource, error) {
	var fields map[string]any
	if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
		return FetchedSource{}, fmt.Errorf("fetched source must be an object")
	}
	config, err := fetchedString(fields, "config")
	if err != nil {
		return FetchedSource{}, err
	}
	if config != string(ConfigRequired) && config != string(ConfigDefault) {
		return FetchedSource{}, fmt.Errorf("fetched source config must be required or default")
	}
	repo, err := fetchedString(fields, "repo")
	if err != nil {
		return FetchedSource{}, err
	}
	mount, err := fetchedString(fields, "mount")
	if err != nil {
		return FetchedSource{}, err
	}
	root, err := fetchedString(fields, "root")
	if err != nil {
		return FetchedSource{}, err
	}
	sha, err := fetchedString(fields, "sha")
	if err != nil {
		return FetchedSource{}, err
	}
	return FetchedSource{Source: Source{Repo: repo, Mount: mount, Config: ConfigMode(config)}, Root: root, SHA: sha}, nil
}

func fetchedString(fields map[string]any, name string) (string, error) {
	value, ok := fields[name].(string)
	if !ok {
		return "", fmt.Errorf("fetched source %s must be a string", name)
	}
	return value, nil
}

func ReadPublishConfig(root string, mode ConfigMode) (PublishConfig, error) {
	file := filepath.Join(root, "docs-bundle.toml")
	content, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && mode == ConfigDefault {
			return PublishConfig{Exclude: []string{}, Sections: []SectionRule{}}, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return PublishConfig{}, fmt.Errorf("required publish config is missing: %s", file)
		}
		return PublishConfig{}, fmt.Errorf("read publish config: %w", err)
	}
	var config PublishConfig
	if err := toml.Unmarshal(content, &config); err != nil {
		return PublishConfig{}, fmt.Errorf("invalid docs-bundle.toml: %w", err)
	}
	if config.Exclude == nil {
		return PublishConfig{}, fmt.Errorf("docs-bundle.toml requires exclude")
	}
	if config.Sections == nil {
		config.Sections = []SectionRule{}
	}
	for index, section := range config.Sections {
		if section.ID == "" || section.Label == "" || section.Route == "" {
			return PublishConfig{}, fmt.Errorf("sections[%d] requires string id, label, and route", index)
		}
		if section.Prefix == nil && section.Files == nil {
			return PublishConfig{}, fmt.Errorf("sections[%d] requires prefix or files", index)
		}
	}
	if config.Packages != nil {
		if config.Packages.Route == "" {
			return PublishConfig{}, fmt.Errorf("packages.route must be a string")
		}
		if config.Packages.Allow == nil {
			return PublishConfig{}, fmt.Errorf("packages.allow must be an array of strings")
		}
		if config.Packages.ReadmeDepth < 0 {
			return PublishConfig{}, fmt.Errorf("packages.readme_depth must be a non-negative integer")
		}
	}
	return config, nil
}

func MatchesAny(file string, patterns []string) (bool, error) {
	for _, pattern := range patterns {
		matched, err := doublestar.Match(pattern, file)
		if err != nil {
			return false, fmt.Errorf("match exclusion pattern %q: %w", pattern, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func AssertFetchedSources(siteRoot string, fetched []FetchedSource) error {
	content, err := os.ReadFile(filepath.Join(siteRoot, "sources.json"))
	if err != nil {
		return fmt.Errorf("read configured source list: %w", err)
	}
	var configured []struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal(content, &configured); err != nil {
		return fmt.Errorf("invalid configured source list: %w", err)
	}
	if configured == nil {
		return fmt.Errorf("configured source list must contain an array")
	}
	fetchedRepos := make(map[string]struct{}, len(fetched))
	for _, source := range fetched {
		fetchedRepos[source.Repo] = struct{}{}
	}
	missing := make([]string, 0)
	for _, source := range configured {
		if _, ok := fetchedRepos[source.Repo]; !ok {
			missing = append(missing, source.Repo)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing fetched source(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func AssertNoRouteCollisions(pages []Page) error {
	byRoute := make(map[string][]Page, len(pages))
	for _, page := range pages {
		byRoute[page.Route] = append(byRoute[page.Route], page)
	}
	pageCount := 0
	var routes []string
	for route, routePages := range byRoute {
		if len(routePages) < 2 {
			continue
		}
		pageCount += len(routePages)
		routes = append(routes, route)
	}
	if pageCount == 0 {
		return nil
	}
	sort.Strings(routes)
	return fmt.Errorf("%d page(s) collide on route(s): %s", pageCount, strings.Join(routes, ", "))
}
