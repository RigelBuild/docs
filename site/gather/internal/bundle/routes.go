// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/RigelBuild/docs/site/gather/internal/slug"
)

var (
	markdownExtensionRE = regexp.MustCompile(`(?i)\.mdx?$`)
	indexSuffixRE       = regexp.MustCompile(`/index$`)
	designDecisionPath  = regexp.MustCompile(`^docs/designs/([^/]+)/decisions/[^/]+/DL-\d+\.md$`)
)

func routeSlug(contentPath string) string {
	trimmed := markdownExtensionRE.ReplaceAllString(contentPath, "")
	segments := strings.Split(trimmed, "/")
	for index, segment := range segments {
		segments[index] = slug.GithubSlug(segment)
	}
	return "/" + indexSuffixRE.ReplaceAllString(strings.Join(segments, "/"), "")
}

func RouteSlug(contentPath string) string {
	return routeSlug(contentPath)
}

func RemapSourceRoutes(pages []Page, source FetchedSource) ([]Page, error) {
	mount := strings.Trim(source.Mount, "/")
	prefix := "/" + source.Repo
	if mount != "" {
		prefix += routeSlug(mount)
	}
	remapped := make([]Page, len(pages))
	copy(remapped, pages)
	for index := range remapped {
		page := &remapped[index]
		if page.Repo != source.Repo {
			return nil, fmt.Errorf("route for %s contains repo %s", source.Repo, page.Repo)
		}
		if !strings.HasPrefix(page.Route, prefix) {
			return nil, fmt.Errorf("route %s is outside source mount %s", page.Route, source.Mount)
		}
		page.Route = strings.TrimPrefix(page.Route, "/"+source.Repo)
		if strings.EqualFold(page.SourcePath, "README.md") {
			page.Route = "/" + mount
			if mount == "" {
				page.Route = "/"
			}
		}
	}
	return remapped, nil
}

func DesignDecisionPathMatch(sourcePath string) []string {
	return designDecisionPath.FindStringSubmatch(sourcePath)
}
