//go:generate go run gen/generate.go
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

// Package slug ports github-slugger@2.0.0 for the route builder.
package slug

import (
	"sort"
	"strings"
	"unicode"
)

// GithubSlug reproduces github-slugger's slug(value) with maintainCase false.
func GithubSlug(value string) string {
	var out strings.Builder
	for _, r := range jsToLower(value) {
		if slugStrip(r) {
			continue
		}
		if r == ' ' {
			out.WriteByte('-')
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// slugStrip reports whether github-slugger removes r.
func slugStrip(r rune) bool {
	i := sort.Search(len(stripRanges), func(i int) bool {
		return stripRanges[i][1] >= r
	})
	return i < len(stripRanges) && stripRanges[i][0] <= r
}

// jsToLower applies JavaScript's lowercase mapping, including dotted I and
// context-sensitive final sigma.
func jsToLower(text string) string {
	if !strings.ContainsAny(text, "Σİ") {
		return strings.ToLower(text)
	}
	runes := []rune(text)
	var lowered strings.Builder
	lowered.Grow(len(text))
	for i, r := range runes {
		switch r {
		case '\u0130':
			lowered.WriteString("i\u0307")
		case '\u03a3':
			if isFinalSigma(runes, i) {
				lowered.WriteRune('\u03c2')
			} else {
				lowered.WriteRune('\u03c3')
			}
		default:
			lowered.WriteRune(unicode.ToLower(r))
		}
	}
	return lowered.String()
}

func isFinalSigma(runes []rune, index int) bool {
	precededByCased := false
	for i := index - 1; i >= 0; i-- {
		if isCaseIgnorable(runes[i]) {
			continue
		}
		precededByCased = isCased(runes[i])
		break
	}
	if !precededByCased {
		return false
	}
	for _, r := range runes[index+1:] {
		if isCaseIgnorable(r) {
			continue
		}
		return !isCased(r)
	}
	return true
}

func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r)
}

func isCaseIgnorable(r rune) bool {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}
	switch r {
	case '\'', ':', '.', '\u00b7', '\u0387', '\u055f', '\u05f4',
		'\u2018', '\u2019', '\u2024', '\u2027', '\ufe13', '\ufe52',
		'\ufe55', '\uff07', '\uff0e', '\uff1a':
		return true
	default:
		return false
	}
}
