// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type linkSpan struct{ start, end int }

var (
	inlineLinkRE    = regexp.MustCompile(`\[[^\[\]]*(?:!\[[^\[\]]*\]\([^)]*\)[^\[\]]*)*\]\(\s*(<[^>]*>|[^\s)]+)(?:\s+[^)]*)?\)`)
	imageLinkRE     = regexp.MustCompile(`!\[[^\]]*\]\(\s*(<[^>]*>|[^\s)]+)(?:\s+[^)]*)?\)`)
	referenceLinkRE = regexp.MustCompile(`(?m)^ {0,3}\[[^\]\n]+\]:[ \t]*(<[^>\n]*>|[^\s<>]+)(?:[ \t]+[^\n]*)?$`)
	bundleFenceOpen = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	githubBlobRE    = regexp.MustCompile(`(?i)^https://github\.com/([^/]+/[^/]+)/blob/main/(.+)$`)
	urlSchemeRE     = regexp.MustCompile(`(?i)^(?:[a-z][a-z\d+.-]*:|//|#)`)
)

func linkTargets(source string) []linkSpan {
	masked := maskCode(source)
	var links []linkSpan
	for _, pattern := range []*regexp.Regexp{inlineLinkRE, imageLinkRE, referenceLinkRE} {
		for _, match := range pattern.FindAllStringSubmatchIndex(masked, -1) {
			start, end := match[2], match[3]
			if start < 0 || end < start {
				continue
			}
			if pattern == referenceLinkRE {
				definition := strings.TrimLeft(masked[match[0]:match[1]], " ")
				target := strings.TrimLeft(masked[start:end], "<")
				if strings.HasPrefix(definition, "[^") || len(target) > 0 && (target[0] == '\'' || target[0] == '"') {
					continue
				}
			}
			links = append(links, linkSpan{start: start, end: end})
		}
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].start == links[j].start {
			return links[i].end < links[j].end
		}
		return links[i].start < links[j].start
	})
	compact := links[:0]
	for _, link := range links {
		if len(compact) == 0 || link.start >= compact[len(compact)-1].end {
			compact = append(compact, link)
		}
	}
	return compact
}

func maskCode(source string) string {
	lines := strings.Split(source, "\n")
	var marker byte
	fenceLength := 0
	for index, line := range lines {
		if fenceLength != 0 {
			lines[index] = strings.Repeat(" ", len(line))
			if closeFence(marker, fenceLength).MatchString(line) {
				fenceLength = 0
			}
			continue
		}
		if match := bundleFenceOpen.FindStringSubmatch(line); match != nil {
			marker = match[1][0]
			fenceLength = len(match[1])
			lines[index] = strings.Repeat(" ", len(line))
			continue
		}
		lines[index] = maskInlineCode(line)
	}
	return strings.Join(lines, "\n")
}

func closeFence(marker byte, length int) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`^ {0,3}%s{%d,}[ \t]*$`, regexp.QuoteMeta(string(marker)), length))
}

func maskInlineCode(line string) string {
	masked := []byte(line)
	for cursor := 0; cursor < len(line); {
		if line[cursor] != '`' {
			cursor++
			continue
		}
		start := cursor
		for cursor < len(line) && line[cursor] == '`' {
			cursor++
		}
		marker := line[start:cursor]
		search := cursor
		for search < len(line) {
			next := strings.IndexByte(line[search:], '`')
			if next < 0 {
				break
			}
			next += search
			end := next
			for end < len(line) && line[end] == '`' {
				end++
			}
			if line[next:end] == marker {
				for index := start; index < end; index++ {
					masked[index] = ' '
				}
				cursor = end
				break
			}
			search = end
		}
	}
	return string(masked)
}

func unwrapTarget(target string) string {
	if strings.HasPrefix(target, "<") && strings.HasSuffix(target, ">") {
		return target[1 : len(target)-1]
	}
	return target
}

func normalizeRouteKey(repo, sourcePath string) string {
	return repo + ":" + strings.TrimPrefix(strings.ReplaceAll(sourcePath, "\\", "/"), "./")
}

func RewriteLinks(source string, page Page, routes map[string]string, root string) string {
	return RewriteLinksWithCallback(source, page, routes, root, nil)
}

func RewriteLinksWithCallback(source string, page Page, routes map[string]string, root string, onMissing func(string)) string {
	links := linkTargets(source)
	if len(links) == 0 {
		return source
	}
	rootPath, _ := filepath.Abs(root)
	var output strings.Builder
	cursor := 0
	for _, span := range links {
		output.WriteString(source[cursor:span.start])
		raw := source[span.start:span.end]
		target := unwrapTarget(raw)
		rewritten := target
		if match := githubBlobRE.FindStringSubmatch(target); match != nil {
			if routed, ok := rewriteGitHubSource(match[1], match[2], routes); ok {
				rewritten = routed
			}
		} else if !urlSchemeRE.MatchString(target) {
			linkPath, suffix := targetParts(target)
			if linkPath == "" {
				output.WriteString(raw)
				cursor = span.end
				continue
			}
			resolved := bundleTargetPath(page.SourcePath, target)
			route := routes[normalizeRouteKey(page.Repo, resolved)]
			if route != "" {
				rewritten = route + suffix
			} else {
				absolute := filepath.Clean(filepath.Join(rootPath, filepath.FromSlash(resolved)))
				inside := absolute == rootPath || strings.HasPrefix(absolute, rootPath+string(filepath.Separator))
				if !inside || !fileExists(absolute) {
					if onMissing != nil {
						onMissing(resolved)
					}
				}
				rewritten = blobURL(page.Repo, resolved, suffix)
			}
		}
		if rewritten == target {
			output.WriteString(raw)
		} else if strings.HasPrefix(rewritten, "https://") || !strings.HasPrefix(raw, "<") {
			output.WriteString(rewritten)
		} else {
			output.WriteByte('<')
			output.WriteString(rewritten)
			output.WriteByte('>')
		}
		cursor = span.end
	}
	output.WriteString(source[cursor:])
	return output.String()
}

func fileExists(file string) bool { _, err := os.Stat(file); return err == nil }

func decodePath(value string) string {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func targetParts(target string) (string, string) {
	at := strings.IndexAny(target, "?#")
	if at < 0 {
		return target, ""
	}
	return target[:at], target[at:]
}
func bundleTargetPath(sourcePath, target string) string {
	linkPath, _ := targetParts(target)
	linkPath = decodePath(linkPath)
	if strings.HasPrefix(linkPath, "/") {
		return path.Clean(strings.TrimPrefix(linkPath, "/"))
	}
	return path.Clean(path.Join(path.Dir(sourcePath), linkPath))
}

func rewriteGitHubSource(repo, remainder string, routes map[string]string) (string, bool) {
	sourcePath, suffix := targetParts(remainder)
	sourcePath = decodePath(sourcePath)
	if route := routes[normalizeRouteKey(repo, sourcePath)]; route != "" {
		return route + suffix, true
	}
	return "", false
}

func blobURL(repo, sourcePath, suffix string) string {
	segments := strings.Split(sourcePath, "/")
	for index, segment := range segments {
		segments[index] = encodeURIComponentPath(segment)
	}
	return fmt.Sprintf("https://github.com/%s/blob/main/%s%s", repo, strings.Join(segments, "/"), suffix)
}

func encodeURIComponentPath(value string) string {
	const hex = "0123456789ABCDEF"
	var output strings.Builder
	output.Grow(len(value))
	for _, b := range []byte(value) {
		safe := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
		safe = safe || strings.ContainsRune("-_.!~*'()", rune(b))
		if safe {
			output.WriteByte(b)
			continue
		}
		output.WriteByte('%')
		output.WriteByte(hex[b>>4])
		output.WriteByte(hex[b&0x0f])
	}
	return output.String()
}

type BrokenLink struct {
	Repo       string
	SourcePath string
	Target     string
}

func FindBrokenLinks(root, repo string, pages []Page) ([]BrokenLink, error) {
	routes := make(map[string]struct{}, len(pages))
	for _, page := range pages {
		routes[normalizeRouteKey(page.Repo, page.SourcePath)] = struct{}{}
	}
	var broken []BrokenLink
	for _, page := range pages {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page.SourcePath)))
		if err != nil {
			return nil, err
		}
		for _, span := range linkTargets(string(content)) {
			target := unwrapTarget(string(content[span.start:span.end]))
			if urlSchemeRE.MatchString(target) {
				continue
			}
			linkPath, _ := targetParts(target)
			if linkPath == "" {
				continue
			}
			resolved := bundleTargetPath(page.SourcePath, target)
			if _, ok := routes[normalizeRouteKey(repo, resolved)]; ok {
				continue
			}
			if !fileExists(filepath.Join(root, filepath.FromSlash(resolved))) {
				broken = append(broken, BrokenLink{Repo: repo, SourcePath: page.SourcePath, Target: resolved})
			}
		}
	}
	return broken, nil
}
