// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const docsRepo = "RigelBuild/docs"

type fence struct {
	marker byte
	length int
}

var (
	sourceFrontmatterRE = regexp.MustCompile(`^\x{FEFF}?---\r?\n([\s\S]*?)\r?\n---\r?\n?`)
	fetchedTitleH1RE    = regexp.MustCompile(`^ {0,3}#[ \t]+\S`)
	fenceRE             = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	htmlTagsRE          = regexp.MustCompile(`(?is)<([A-Za-z][\w-]*)\b[^>]*>(.*?)</([A-Za-z][\w-]*)\s*>`)
	inlineCodeTitleRE   = regexp.MustCompile("`+[^`]+`+")
	frontMatterRE       = regexp.MustCompile(`^\x{FEFF}?---\r?\n[\s\S]*?\r?\n---\r?\n?`)
	headingPrefixRE     = regexp.MustCompile(`^ {0,3}#[ \t]+`)
	headingSuffixRE     = regexp.MustCompile(` +#+ *$`)
	markdownFileRE      = regexp.MustCompile(`(?i)\.md$`)
	filenameSeparatorRE = regexp.MustCompile(`[-_]+`)
	titleImageRE        = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	titleLinkRE         = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	titleReferenceRE    = regexp.MustCompile(`\[([^\]]+)\]\[[^\]]*\]`)
	titleEscapeRE       = regexp.MustCompile(`\\([\\` + "`" + `*_{}\[\]()#+.!<>|~-])`)
	titleSpaceRE        = regexp.MustCompile(`\s+`)
)

func StripSourceFrontmatter(markdown, label string, logger *slog.Logger) (string, map[string]any) {
	match := sourceFrontmatterRE.FindStringSubmatchIndex(markdown)
	if match == nil {
		return markdown, nil
	}
	fields := make(map[string]any)
	if err := yaml.Unmarshal([]byte(markdown[match[2]:match[3]]), &fields); err != nil {
		logger.Warn("unparsable frontmatter", "label", label, "err", err)
		return markdown[match[1]:], nil
	}
	return markdown[match[1]:], fields
}

func PageTitle(body string, fields map[string]any, sourcePath string) string {
	if firstH1(body) < 0 {
		for _, key := range []string{"title", "page_title"} {
			if title, ok := fields[key].(string); ok {
				return title
			}
		}
	}
	return ExtractTitle(body, sourcePath)
}

func ExtractTitle(source, sourcePath string) string {
	if index := firstH1(source); index >= 0 {
		line := strings.Split(source, "\n")[index]
		match := headingPrefixRE.FindStringIndex(line)
		if match != nil && fetchedTitleH1RE.MatchString(line) {
			value := line[match[1]:]
			return headingText(headingSuffixRE.ReplaceAllString(value, ""))
		}
	}
	filename := strings.ReplaceAll(sourcePath, "\\", "/")
	filename = filename[strings.LastIndex(filename, "/")+1:]
	filename = markdownFileRE.ReplaceAllString(filename, "")
	filename = strings.TrimSpace(filenameSeparatorRE.ReplaceAllString(filename, " "))
	if filename == "" {
		return "Untitled"
	}
	first, size := utf8.DecodeRuneInString(filename)
	return string(unicode.ToUpper(first)) + filename[size:]
}

func StripFirstH1(source string) string {
	lines := strings.Split(source, "\n")
	heading := firstH1(source)
	if heading < 0 {
		return source
	}
	lines = append(lines[:heading], lines[heading+1:]...)
	return strings.Join(lines, "\n")
}

func firstH1(source string) int {
	lines := strings.Split(source, "\n")
	var open *fence
	for index, line := range lines {
		fenceMarker := fenceRE.FindString(line)
		if open != nil {
			if fenceMarker != "" && fenceMarker[0] == open.marker && isFenceClose(line, open.marker, open.length) {
				open = nil
			}
			continue
		}
		if fenceMarker != "" {
			open = &fence{marker: fenceMarker[0], length: len(strings.TrimLeft(fenceMarker, " "))}
			continue
		}
		if fetchedTitleH1RE.MatchString(line) {
			return index
		}
	}
	return -1
}

func isFenceClose(line string, marker byte, length int) bool {
	if len(line)-len(strings.TrimLeft(line, " ")) > 3 {
		return false
	}
	line = strings.TrimLeft(line, " ")
	count := 0
	for count < len(line) && line[count] == marker {
		count++
	}
	return count >= length && strings.Trim(line[count:], " \t") == ""
}

func headingText(value string) string {
	codeSpans := make([]string, 0, 2)
	marker := "DOCSCODESPAN"
	for strings.Contains(value, marker) {
		marker += "X"
	}
	value = inlineCodeTitleRE.ReplaceAllStringFunc(value, func(span string) string {
		code := strings.Trim(span, "`")
		index := len(codeSpans)
		codeSpans = append(codeSpans, code)
		return fmt.Sprintf("%s%dZ", marker, index)
	})
	value = titleImageRE.ReplaceAllString(value, "$1")
	value = titleLinkRE.ReplaceAllString(value, "$1")
	value = titleReferenceRE.ReplaceAllString(value, "$1")
	value = htmlTagsRE.ReplaceAllStringFunc(value, stripHTMLTagPair)
	value = stripEmphasis(value)
	value = titleEscapeRE.ReplaceAllString(value, "$1")
	for index, code := range codeSpans {
		value = strings.ReplaceAll(value, fmt.Sprintf("%s%dZ", marker, index), code)
	}
	return strings.TrimSpace(titleSpaceRE.ReplaceAllString(value, " "))
}

func stripEmphasis(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for index := 0; index < len(value); {
		marker := ""
		for _, candidate := range []string{"**", "__", "~~", "*", "_"} {
			if strings.HasPrefix(value[index:], candidate) {
				marker = candidate
				break
			}
		}
		if marker == "" || index+len(marker) >= len(value) || isJSSpace(rune(value[index+len(marker)])) {
			out.WriteByte(value[index])
			index++
			continue
		}
		contentStart := index + len(marker)
		close := strings.Index(value[contentStart:], marker)
		if close <= 0 {
			out.WriteByte(value[index])
			index++
			continue
		}
		contentEnd := contentStart + close
		if isJSSpace(rune(value[contentEnd-1])) {
			out.WriteByte(value[index])
			index++
			continue
		}
		out.WriteString(value[contentStart:contentEnd])
		index = contentEnd + len(marker)
	}
	return out.String()
}

func stripHTMLTagPair(value string) string {
	match := htmlTagsRE.FindStringSubmatch(value)
	if len(match) != 4 || !strings.EqualFold(match[1], match[3]) {
		return value
	}
	return match[2]
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

func jsTrim(value string) string { return strings.TrimFunc(value, isJSSpace) }

func frontmatter(title, repo, sourcePath string) string {
	escaped := strings.ReplaceAll(strings.ReplaceAll(title, `\`, `\\`), `"`, `\"`)
	editURL := ""
	if repo != "" && sourcePath != "" {
		editURL = fmt.Sprintf("editUrl: \"https://github.com/%s/edit/main/%s\"\n", repo, sourcePath)
	}
	return fmt.Sprintf("---\ntitle: \"%s\"\n%s---\n", escaped, editURL)
}
