// SPDX-License-Identifier: MIT OR Apache-2.0

package slug

import "testing"

func TestGithubSlug(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "lowercases and hyphenates spaces", input: "Hello World", want: "hello-world"},
		{name: "strips changed Thai code point", input: "a\u0ECEb", want: "ab"},
		{name: "strips changed Mongolian code point", input: "a\u180Fb", want: "ab"},
		{name: "strips changed Georgian code point", input: "a\u1C89b", want: "ab"},
		{name: "strips punctuation", input: "a.b!c", want: "abc"},
		{name: "preserves unicode letters", input: "café", want: "café"},
		{name: "strips private-use code points", input: "a\uE000b", want: "ab"},
		{name: "preserves digits and dashes", input: "v1-beta", want: "v1-beta"},
		{name: "maps dotted I like JavaScript", input: "\u0130", want: "i\u0307"},
		{name: "uses final sigma like JavaScript", input: "ΑΣ", want: "ας"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := GithubSlug(test.input); got != test.want {
				t.Fatalf("GithubSlug(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
