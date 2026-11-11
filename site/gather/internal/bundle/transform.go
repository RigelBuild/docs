// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/RigelBuild/meissa/go/ledger"
)

var (
	designLinkRE = regexp.MustCompile(`(!?\[[^\][\n]*)?(\]\()([^\s)]+)(\s+"[^"]*")?(\))`)
	designScheme = regexp.MustCompile(`(?i)^(?:[a-z][a-z\d+.-]*:|/|#)`)
)

func Frontmatter(title, repo, sourcePath string) string { return frontmatter(title, repo, sourcePath) }

func Transform(source, sourcePath string, gathered map[string]string) (string, error) {
	if designDecisionPath.MatchString(sourcePath) {
		row, err := ledger.ParseDecisionFile(sourcePath, []byte(source))
		if err != nil {
			return "", err
		}
		body := frontMatterRE.ReplaceAllString(source, "")
		extra := ""
		if jsTrim(body) != "" {
			extra = "\n" + body
		}
		page := fmt.Sprintf("%s\n\n**Status:** %s\n\n**Record:** [%s](%s)\n%s", row.Decision, row.Status, row.RecordRaw, row.RecordRaw, extra)
		return frontmatter(row.ID, docsRepo, sourcePath) + rewriteDesignLinks(page, sourcePath, gathered), nil
	}
	return frontmatter(ExtractTitle(source, sourcePath), docsRepo, sourcePath) + rewriteDesignLinks(StripFirstH1(source), sourcePath, gathered), nil
}

func BuildDecisionIndex(surface string, rows []ledger.DecisionRow, gathered map[string]string) string {
	byArea := make(map[string][]ledger.DecisionRow)
	for _, row := range rows {
		byArea[path.Dir(row.Path)] = append(byArea[path.Dir(row.Path)], row)
	}
	areas := make([]string, 0, len(byArea))
	for area := range byArea {
		areas = append(areas, area)
	}
	sort.Strings(areas)
	sections := make([]string, 0, len(areas))
	for _, area := range areas {
		sections = append(sections, fmt.Sprintf("## %s\n\n%s", path.Base(area), rewriteDesignLinks(ledger.RenderDecisionIndex(byArea[area]), area+"/index.md", gathered)))
	}
	return fmt.Sprintf("%s\nEvery %s decision, by area. Generated from the decision files at build time.\n\n%s", frontmatter(surface+" decisions", "", ""), surface, strings.Join(sections, "\n"))
}

func rewriteDesignLinks(source, sourcePath string, gathered map[string]string) string {
	return designLinkRE.ReplaceAllStringFunc(source, func(match string) string {
		parts := designLinkRE.FindStringSubmatch(match)
		label, open, target, title, closing := parts[1], parts[2], parts[3], parts[4], parts[5]
		if designScheme.MatchString(target) || strings.HasPrefix(label, "!") {
			return match
		}
		linkPath, suffix := splitLinkSuffix(target)
		if linkPath == "" {
			return match
		}
		resolved := normalizeDesignPath(path.Join(path.Dir(sourcePath), linkPath))
		prefix := label + open
		if strings.EqualFold(path.Ext(resolved), ".md") {
			if _, ok := gathered[resolved]; ok {
				return prefix + routeSlug(strings.TrimPrefix(resolved, "docs/")) + suffix + title + closing
			}
		}
		kind := "blob"
		if strings.HasSuffix(linkPath, "/") {
			kind = "tree"
		}
		return prefix + fmt.Sprintf("https://github.com/%s/%s/main/%s%s", docsRepo, kind, resolved, suffix) + title + closing
	})
}

func splitLinkSuffix(target string) (string, string) {
	if index := strings.IndexAny(target, "?#"); index >= 0 {
		return target[:index], target[index:]
	}
	return target, ""
}

func normalizeDesignPath(value string) string {
	var segments []string
	for _, segment := range strings.Split(value, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, segment)
		}
	}
	return strings.Join(segments, "/")
}
