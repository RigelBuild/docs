// SPDX-License-Identifier: ISC
// Copyright (c) 2015 Dan Flettre
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR
// IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	ranges, err := excludedRanges()
	if err != nil {
		fatal(err)
	}

	file, err := os.Create("ranges_generated.go")
	if err != nil {
		fatal(err)
	}

	out := bufio.NewWriter(file)
	if err := writeHeader(out); err != nil {
		fatal(err)
	}
	for _, bounds := range ranges {
		if _, err := fmt.Fprintf(out, "\t{0x%04x, 0x%04x},\n", bounds[0], bounds[1]); err != nil {
			fatal(err)
		}
	}
	if _, err := fmt.Fprintln(out, "}"); err != nil {
		fatal(err)
	}
	if err := out.Flush(); err != nil {
		fatal(err)
	}
	if err := file.Close(); err != nil {
		fatal(err)
	}
}

func excludedRanges() ([][2]rune, error) {
	data, err := os.ReadFile("gen/regex.js")
	if err != nil {
		return nil, fmt.Errorf("read github-slugger regex: %w", err)
	}
	text := string(data)
	start := strings.Index(text, "regex = /")
	if start < 0 {
		return nil, fmt.Errorf("github-slugger regex declaration not found")
	}
	text = text[start+len("regex = /"):]
	end := strings.LastIndex(text, "/g")
	if end < 0 {
		return nil, fmt.Errorf("github-slugger regex terminator not found")
	}
	pattern := text[:end]
	classEnd := classEndIndex(pattern)
	if len(pattern) == 0 || pattern[0] != '[' || classEnd < 0 {
		return nil, fmt.Errorf("github-slugger regex character class not found")
	}

	ranges := make([][2]rune, 0, 736)
	if err := parseClass(pattern[1:classEnd], &ranges); err != nil {
		return nil, fmt.Errorf("parse github-slugger character class: %w", err)
	}
	if err := parseSurrogateAlternations(pattern[classEnd+1:], &ranges); err != nil {
		return nil, fmt.Errorf("parse github-slugger surrogate alternations: %w", err)
	}
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i][0] < ranges[j][0]
	})

	merged := make([][2]rune, 0, len(ranges))
	for _, current := range ranges {
		previousEnd := rune(-1)
		if len(merged) > 0 {
			previousEnd = merged[len(merged)-1][1]
		}
		isSurrogateGap := (previousEnd == 0xD7FF && current[0] == 0xD800) ||
			(previousEnd == 0xDFFF && current[0] == 0xE000)
		if len(merged) > 0 && current[0] <= previousEnd+1 && !isSurrogateGap {
			if current[1] > merged[len(merged)-1][1] {
				merged[len(merged)-1][1] = current[1]
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged, nil
}

func classEndIndex(pattern string) int {
	escaped := false
	for i := 1; i < len(pattern); i++ {
		if escaped {
			escaped = false
			continue
		}
		if pattern[i] == '\\' {
			escaped = true
			continue
		}
		if pattern[i] == ']' {
			return i
		}
	}
	return -1
}

func parseClass(class string, ranges *[][2]rune) error {
	for i := 0; i < len(class); {
		first, next, err := parseClassRune(class, i)
		if err != nil {
			return err
		}
		i = next
		last := first
		if i < len(class) && class[i] == '-' {
			last, i, err = parseClassRune(class, i+1)
			if err != nil {
				return err
			}
		}
		if last < first {
			return fmt.Errorf("descending range U+%04X-U+%04X", first, last)
		}
		*ranges = append(*ranges, [2]rune{first, last})
	}
	return nil
}

func parseClassRune(class string, index int) (rune, int, error) {
	if index >= len(class) {
		return 0, index, fmt.Errorf("unexpected end of character class")
	}
	if class[index] != '\\' {
		return rune(class[index]), index + 1, nil
	}
	if index+1 >= len(class) {
		return 0, index, fmt.Errorf("incomplete escape")
	}
	switch class[index+1] {
	case '0':
		return 0, index + 2, nil
	case 'x':
		return parseUnicodeEscape(class, index, 2)
	case 'u':
		return parseUnicodeEscape(class, index, 4)
	default:
		return rune(class[index+1]), index + 2, nil
	}
}

func parseUnicodeEscape(text string, index, digits int) (rune, int, error) {
	value, next, err := parseHexEscape(text, index, digits)
	if err != nil {
		return 0, next, err
	}
	if value >= 0xD800 && value <= 0xDFFF {
		return 0, index, fmt.Errorf("lone surrogate U+%04X in character class", value)
	}
	return value, next, nil
}

func parseHexEscape(text string, index, digits int) (rune, int, error) {
	end := index + 2 + digits
	if end > len(text) {
		return 0, index, fmt.Errorf("incomplete hexadecimal escape")
	}
	value, err := strconv.ParseUint(text[index+2:end], 16, 32)
	if err != nil {
		return 0, index, fmt.Errorf("invalid hexadecimal escape: %w", err)
	}
	return rune(value), end, nil
}

func parseSurrogateAlternations(text string, ranges *[][2]rune) error {
	for len(text) > 0 {
		if !strings.HasPrefix(text, "|") {
			return fmt.Errorf("unexpected text after character class: %q", text[:min(len(text), 24)])
		}
		text = text[1:]
		if strings.HasPrefix(text, "[") {
			highs := make([]rune, 0, 16)
			i := 1
			for i < len(text) && text[i] != ']' {
				high, next, err := parseHexEscape(text, i, 4)
				if err != nil {
					return err
				}
				i = next
				last := high
				if i < len(text) && text[i] == '-' {
					last, i, err = parseHexEscape(text, i+1, 4)
					if err != nil {
						return err
					}
				}
				if high < 0xD800 || last > 0xDBFF || last < high {
					return fmt.Errorf("invalid high-surrogate range U+%04X-U+%04X", high, last)
				}
				for value := high; value <= last; value++ {
					highs = append(highs, value)
				}
			}
			if i >= len(text) || text[i] != ']' {
				return fmt.Errorf("unterminated high-surrogate class")
			}
			text = text[i+1:]
			if !strings.HasPrefix(text, "[\\uDC00-\\uDFFF]") {
				return fmt.Errorf("expected full low-surrogate class")
			}
			text = text[len("[\\uDC00-\\uDFFF]"):]
			for _, high := range highs {
				*ranges = append(*ranges, [2]rune{surrogatePair(high, 0xDC00), surrogatePair(high, 0xDFFF)})
			}
			continue
		}
		if !strings.HasPrefix(text, "\\u") {
			return fmt.Errorf("expected high-surrogate escape")
		}
		high, next, err := parseHexEscape(text, 0, 4)
		if err != nil {
			return err
		}
		if high < 0xD800 || high > 0xDBFF {
			return fmt.Errorf("invalid high surrogate U+%04X", high)
		}
		text = text[next:]
		if !strings.HasPrefix(text, "[") {
			return fmt.Errorf("expected low-surrogate class")
		}
		text = text[1:]
		for len(text) > 0 && text[0] != ']' {
			low, next, err := parseHexEscape(text, 0, 4)
			if err != nil {
				return err
			}
			text = text[next:]
			last := low
			if len(text) > 0 && text[0] == '-' {
				last, next, err = parseHexEscape(text, 1, 4)
				if err != nil {
					return err
				}
				text = text[next:]
			}
			if low < 0xDC00 || last > 0xDFFF || last < low {
				return fmt.Errorf("invalid low-surrogate range U+%04X-U+%04X", low, last)
			}
			*ranges = append(*ranges, [2]rune{surrogatePair(high, low), surrogatePair(high, last)})
		}
		if len(text) == 0 || text[0] != ']' {
			return fmt.Errorf("unterminated low-surrogate class")
		}
		text = text[1:]
	}
	return nil
}

func surrogatePair(high, low rune) rune {
	return 0x10000 + (high-0xD800)*0x400 + (low - 0xDC00)
}

func writeHeader(out *bufio.Writer) error {
	for _, line := range []string{
		"// SPDX-License-Identifier: ISC",
		"// Code generated by generate.go; DO NOT EDIT.",
		"// Copyright (c) 2015 Dan Flettre <fletd01@yahoo.com>",
		"//",
		"// Permission to use, copy, modify, and/or distribute this software for any",
		"// purpose with or without fee is hereby granted, provided that the above",
		"// copyright notice and this permission notice appear in all copies.",
		"//",
		"// THE SOFTWARE IS PROVIDED \"AS IS\" AND THE AUTHOR DISCLAIMS ALL WARRANTIES",
		"// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF",
		"// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR",
		"// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES",
		"// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN",
		"// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR",
		"// IN CONNECTION WITH THE USE OR PERFORMANCE OF THE SOFTWARE.",
		"",
		"package slug",
		"",
		"// stripRanges is sorted and non-overlapping.",
		"var stripRanges = [...][2]rune{",
	} {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
