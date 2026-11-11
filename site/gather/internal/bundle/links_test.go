// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRewriteLinksAndFindBrokenLinks(t *testing.T) {
	t.Run("rewrites routed links and reports missing local links", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("[next](next.md) [missing](absent.md#part)"), 0o644); err != nil {
			t.Fatal(err)
		}
		page := Page{Repo: "acme/widgets", SourcePath: "index.md"}
		routes := map[string]string{"acme/widgets:next.md": "/guide/next"}
		var missing []string
		got := RewriteLinksWithCallback("[next](next.md) [missing](absent.md#part)", page, routes, root, func(target string) {
			missing = append(missing, target)
		})
		if got != "[next](/guide/next) [missing](https://github.com/acme/widgets/blob/main/absent.md#part)" || len(missing) != 1 || missing[0] != "absent.md" {
			t.Fatalf("rewrite = %q, missing = %#v", got, missing)
		}
	})
	t.Run("rewrites a link wrapping an image", func(t *testing.T) {
		page := Page{Repo: "acme/widgets", SourcePath: "index.md"}
		routes := map[string]string{"acme/widgets:next.md": "/guide/next"}
		got := RewriteLinks("[![badge](img.svg)](next.md)", page, routes, t.TempDir())
		want := "[![badge](https://github.com/acme/widgets/blob/main/img.svg)](/guide/next)"
		if got != want {
			t.Fatalf("rewrite = %q, want %q", got, want)
		}
	})
	t.Run("rewrites reference definitions outside fences", func(t *testing.T) {
		page := Page{Repo: "acme/widgets", SourcePath: "docs/index.md"}
		routes := map[string]string{"acme/widgets:docs/next.md": "/guide/next"}
		input := "[ref]: next.md\n```\n[hidden]: missing.md\n```\n`[inline](missing.md)`"
		got := RewriteLinks(input, page, routes, t.TempDir())
		want := "[ref]: /guide/next\n```\n[hidden]: missing.md\n```\n`[inline](missing.md)`"
		if got != want {
			t.Fatalf("rewrite = %q, want %q", got, want)
		}
	})
	t.Run("resolves root-absolute links and encoded paths", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "docs", "my doc.md"), []byte("# Doc"), 0o644); err != nil {
			t.Fatal(err)
		}
		page := Page{Repo: "acme/widgets", SourcePath: "docs/index.md"}
		routes := map[string]string{"acme/widgets:docs/next.md": "/guide/next", "acme/widgets:docs/my doc.md": "/my-doc"}
		got := RewriteLinks("[absolute](/docs/next.md) [encoded](my%20doc.md)", page, routes, root)
		if got != "[absolute](/guide/next) [encoded](/my-doc)" {
			t.Fatalf("rewrite = %q", got)
		}
	})
	t.Run("rewrites bundled GitHub blob links but preserves external targets", func(t *testing.T) {
		page := Page{Repo: "acme/widgets", SourcePath: "docs/index.md"}
		routes := map[string]string{"acme/widgets:docs/next.md": "/guide/next"}
		input := "[next](https://github.com/acme/widgets/blob/main/docs/next.md#usage) [ext](<https://example.com/a b>) [fragment](<#sec>)"
		got := RewriteLinks(input, page, routes, t.TempDir())
		want := "[next](/guide/next#usage) [ext](<https://example.com/a b>) [fragment](<#sec>)"
		if got != want {
			t.Fatalf("rewrite = %q, want %q", got, want)
		}
	})
	t.Run("preserves unrouted GitHub and tree targets", func(t *testing.T) {
		input := "[external](https://github.com/Acme/Widgets/blob/main/My%20File%2B.md?x=1) [tree](https://github.com/acme/widgets/tree/main/docs/page.md)"
		got := RewriteLinks(input, Page{Repo: "acme/widgets", SourcePath: "index.md"}, map[string]string{}, t.TempDir())
		if got != input {
			t.Fatalf("unrouted target rewrite = %q, want %q", got, input)
		}
	})
	t.Run("escapes encodeURIComponent reserved path bytes", func(t *testing.T) {
		got := RewriteLinks("[x](<a+b&c=d$e,f;g:h@i.md>)", Page{Repo: "acme/widgets", SourcePath: "index.md"}, map[string]string{}, t.TempDir())
		want := "[x](https://github.com/acme/widgets/blob/main/a%2Bb%26c%3Dd%24e%2Cf%3Bg%3Ah%40i.md)"
		if got != want {
			t.Fatalf("escaped path = %q, want %q", got, want)
		}
	})
	t.Run("keeps valid reference before a quoted reference", func(t *testing.T) {
		input := "[a]: guide.md\n\n[b]: \"quoted\"\n"
		got := RewriteLinks(input, Page{Repo: "acme/widgets", SourcePath: "index.md"}, map[string]string{"acme/widgets:guide.md": "/guide"}, t.TempDir())
		want := "[a]: /guide\n\n[b]: \"quoted\"\n"
		if got != want {
			t.Fatalf("reference rewrite = %q, want %q", got, want)
		}
	})
	t.Run("preserves empty target paths", func(t *testing.T) {
		input := "[query](?x)"
		if got := RewriteLinks(input, Page{Repo: "acme/widgets", SourcePath: "docs/index.md"}, map[string]string{}, t.TempDir()); got != input {
			t.Fatalf("empty target rewrite = %q", got)
		}
	})
	t.Run("ignores nested image links in code and reports missing paths", func(t *testing.T) {
		root := t.TempDir()
		input := "![a [b](x.png)\n```md\n[hidden](missing.md)\n```"
		if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(input), 0o644); err != nil {
			t.Fatal(err)
		}
		page := Page{Repo: "acme/widgets", SourcePath: "index.md"}
		got := RewriteLinks(input, page, map[string]string{}, root)
		if got != "![a [b](https://github.com/acme/widgets/blob/main/x.png)\n```md\n[hidden](missing.md)\n```" {
			t.Fatalf("rewrite = %q", got)
		}
		broken, err := FindBrokenLinks(root, page.Repo, []Page{page})
		if err != nil || len(broken) != 1 || broken[0].Target != "x.png" {
			t.Fatalf("FindBrokenLinks = %#v, %v", broken, err)
		}
	})
}
