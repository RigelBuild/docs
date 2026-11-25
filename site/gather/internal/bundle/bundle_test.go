// SPDX-License-Identifier: MIT OR Apache-2.0

package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleConfigAndManifest(t *testing.T) {
	t.Run("parses Markdown exclusion config and applies glob exclusions", func(t *testing.T) {
		config := PublishConfig{Exclude: []string{"private/**", "**/outputs/**", "**/CHANGELOG.md"}, Sections: []SectionRule{}}
		for _, value := range []struct {
			path string
			want bool
		}{{"private/notes.md", true}, {"public/guide.md", false}, {"CHANGELOG.md", true}, {"nested/outputs/file.md", true}} {
			got, err := MatchesAny(value.path, config.Exclude)
			if err != nil || got != value.want {
				t.Errorf("MatchesAny(%q) = %t, %v", value.path, got, err)
			}
		}
	})
	t.Run("skips private pages and excluded directories", func(t *testing.T) {
		for _, file := range []string{"_partial.md", "guide/_partial.md", "_internal/guide.md", "node_modules/pkg/readme.md", "dist/guide.md", "outputs/guide.md"} {
			if IsGatherable(file) {
				t.Errorf("IsGatherable(%q) = true", file)
			}
		}
		if !IsGatherable("guide/public.md") {
			t.Fatal("public page was excluded")
		}
		files, err := eligibleSourceFiles([]string{"private/notes.md", "public/guide.md"}, []string{"private/**"})
		if err != nil || len(files) != 1 || files[0] != "public/guide.md" {
			t.Fatalf("eligibleSourceFiles = %v, %v", files, err)
		}
	})
	t.Run("reads fetched source records and validates required fields", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(root, "fetched.json")
		valid := `[{"repo":"acme/widgets","mount":"/widgets/","config":"default","root":"/tmp/widgets","sha":"abc123"}]`
		if err := os.WriteFile(file, []byte(valid), 0o644); err != nil {
			t.Fatal(err)
		}
		sources, err := ReadFetched(file)
		if err != nil || len(sources) != 1 || sources[0].Repo != "acme/widgets" {
			t.Fatalf("ReadFetched = %#v, %v", sources, err)
		}
		invalidInputs := []string{
			`null`, `{}`, `[null]`, `[1]`,
			`[{"repo":"acme/widgets"}]`,
			`[{"repo":"acme/widgets","mount":"/x","config":"invalid","root":"/tmp","sha":"abc"}]`,
			`[{"repo":1,"mount":"/x","config":"default","root":"/tmp","sha":"abc"}]`,
			`[{"repo":"acme/widgets","mount":null,"config":"default","root":"/tmp","sha":"abc"}]`,
		}
		for _, invalid := range invalidInputs {
			if err := os.WriteFile(file, []byte(invalid), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadFetched(file); err == nil {
				t.Errorf("ReadFetched(%s) unexpectedly succeeded", invalid)
			}
		}
		if err := os.WriteFile(file, []byte(`[{"repo":"acme/widgets","mount":"","config":"default","root":"/tmp","sha":"abc123"}]`), 0o644); err != nil {
			t.Fatal(err)
		}
		sources, err = ReadFetched(file)
		if err != nil || len(sources) != 1 || sources[0].Mount != "" {
			t.Fatalf("ReadFetched accepted empty mount = %#v, %v", sources, err)
		}
		if sources, err := ReadFetched(filepath.Join(root, "missing.json")); err != nil || len(sources) != 0 {
			t.Fatalf("missing fetched list = %#v, %v", sources, err)
		}
	})
	t.Run("requires exclude and validates section fields", func(t *testing.T) {
		root := t.TempDir()
		for _, value := range []struct{ name, text, want string }{
			{"missing exclude", "[packages]\nroute=\"/x\"\nallow=[]\nreadme_depth=0", "requires exclude"},
			{"missing section route", "exclude=[]\n[[sections]]\nid=\"guide\"\nlabel=\"Guide\"\nprefix=\"docs/\"", "requires string id, label, and route"},
			{"missing section selector", "exclude=[]\n[[sections]]\nid=\"guide\"\nlabel=\"Guide\"\nroute=\"/guide\"", "requires prefix or files"},
		} {
			t.Run(value.name, func(t *testing.T) {
				if err := os.WriteFile(filepath.Join(root, "docs-bundle.toml"), []byte(value.text), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := ReadPublishConfig(root, ConfigRequired); err == nil || !strings.Contains(err.Error(), value.want) {
					t.Fatalf("ReadPublishConfig error = %v; want %q", err, value.want)
				}
			})
		}
	})
	t.Run("routes a page under a section prefix without a trailing slash", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "docs", "guide"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "docs", "guide", "intro.md"), []byte("# Intro"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "docs", "guidelines.md"), []byte("# Guidelines"), 0o644); err != nil {
			t.Fatal(err)
		}
		prefix := "docs/guide"
		source := FetchedSource{Source: Source{Repo: "acme/widgets", Config: ConfigRequired}, Root: root}
		config := PublishConfig{Exclude: []string{}, Sections: []SectionRule{{ID: "guide", Label: "Guide", Route: "/guide", Prefix: &prefix}}}
		if _, err := CollectPages(source, config); err == nil || !strings.Contains(err.Error(), "unassigned page: docs/guidelines.md") {
			t.Fatalf("CollectPages error = %v", err)
		}
		if err := os.Remove(filepath.Join(root, "docs", "guidelines.md")); err != nil {
			t.Fatal(err)
		}
		pages, err := CollectPages(source, config)
		if err != nil || len(pages) != 1 || pages[0].Route != "/guide/intro" {
			t.Fatalf("CollectPages = %#v, %v", pages, err)
		}
	})
	t.Run("routes catch-all and explicit files without collisions", func(t *testing.T) {
		root := t.TempDir()
		for _, file := range []string{"README.md", "docs/README.md", "apps/widget/README.md", "tools/item/AGENTS.md", "notes/guide.md"} {
			full := filepath.Join(root, file)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("# Page"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		packages := &PackageRules{Route: "/packages", Allow: []string{"notes/"}, ReadmeDepth: 3, Nested: []string{"apps"}}
		config := PublishConfig{Exclude: []string{}, Sections: []SectionRule{{ID: "contributing", Label: "Contributing", Route: "/contributing", Files: []string{"README.md", "docs/README.md"}}}, Packages: packages}
		pages, err := CollectPages(FetchedSource{Source: Source{Repo: "acme/widgets", Config: ConfigRequired}, Root: root}, config)
		if err != nil {
			t.Fatal(err)
		}
		if err := AssertNoRouteCollisions(pages); err != nil {
			t.Fatalf("AssertNoRouteCollisions = %v", err)
		}
		for _, file := range []string{"apps/widget/stray.md", "tools/item/stray.md"} {
			full := filepath.Join(root, file)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("# Page"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := CollectPages(FetchedSource{Source: Source{Repo: "acme/widgets", Config: ConfigRequired}, Root: root}, config); err == nil {
				t.Fatalf("CollectPages accepted stray package page %s", file)
			}
			if err := os.Remove(full); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("throws when two pages claim one route", func(t *testing.T) {
		err := AssertNoRouteCollisions([]Page{{Repo: "a", SourcePath: "a.md", Route: "/guide"}, {Repo: "b", SourcePath: "b.md", Route: "/guide"}})
		if err == nil || !strings.Contains(err.Error(), "2 page(s) collide on route(s): /guide") {
			t.Fatalf("collision error = %v", err)
		}
	})
	t.Run("throws when package docs exceed configured allow rules", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "packages", "widgets"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "packages", "widgets", "README.md"), []byte("# Widgets"), 0o644); err != nil {
			t.Fatal(err)
		}
		config := PublishConfig{Exclude: []string{}, Sections: []SectionRule{}, Packages: &PackageRules{Route: "/packages", Allow: []string{}, ReadmeDepth: 3}}
		if _, err := CollectPages(FetchedSource{Source: Source{Repo: "acme/widgets", Config: ConfigRequired}, Root: root}, config); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "stray.md"), []byte("# Stray"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := CollectPages(FetchedSource{Source: Source{Repo: "acme/widgets", Config: ConfigRequired}, Root: root}, config); err == nil || !strings.Contains(err.Error(), "stray package page: stray.md") {
			t.Fatalf("stray package error = %v", err)
		}
	})
	t.Run("rejects root readmes not claimed by a section", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Root"), 0o644); err != nil {
			t.Fatal(err)
		}
		config := PublishConfig{Exclude: []string{}, Sections: []SectionRule{}, Packages: &PackageRules{Route: "/packages", Allow: []string{}, ReadmeDepth: 3}}
		if _, err := CollectPages(FetchedSource{Source: Source{Repo: "acme/widgets", Config: ConfigRequired}, Root: root}, config); err == nil || !strings.Contains(err.Error(), "stray package page: README.md") {
			t.Fatalf("root README error = %v", err)
		}
	})
	t.Run("rewrites and checks reference targets containing apostrophes", func(t *testing.T) {
		root := t.TempDir()
		input := "[k]: docs/don't.md\n"
		if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(input), 0o644); err != nil {
			t.Fatal(err)
		}
		page := Page{Repo: "acme/widgets", SourcePath: "index.md"}
		if got := RewriteLinks(input, page, map[string]string{"acme/widgets:docs/don't.md": "/guide"}, root); got != "[k]: /guide\n" {
			t.Fatalf("rewrite = %q", got)
		}
		broken, err := FindBrokenLinks(root, page.Repo, []Page{page})
		if err != nil || len(broken) != 1 || broken[0].Target != "docs/don't.md" {
			t.Fatalf("FindBrokenLinks = %#v, %v", broken, err)
		}
	})
	t.Run("checks configured source lists by repo, not mount", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "sources.json"), []byte(`[{"repo":"acme/widgets","mount":"/widgets/","config":"default"},{"repo":"acme/docs","mount":"/docs/","config":"required"}]`), 0o644); err != nil {
			t.Fatal(err)
		}
		fetched := []FetchedSource{{Source: Source{Repo: "acme/widgets", Mount: "widgets", Config: ConfigDefault}}, {Source: Source{Repo: "acme/docs", Mount: "docs", Config: ConfigRequired}}}
		if err := AssertFetchedSources(root, fetched); err != nil {
			t.Fatal(err)
		}
		if err := AssertFetchedSources(root, fetched[:1]); err == nil || !strings.Contains(err.Error(), "acme/docs") {
			t.Fatalf("missing source error = %v", err)
		}
	})
	t.Run("uses GitHub slug rules for every route segment", func(t *testing.T) {
		if got := routeSlug("Guides/Hello, World!.md"); got != "/guides/hello-world" {
			t.Fatalf("routeSlug = %q", got)
		}
	})
	t.Run("handles index and MDX routes", func(t *testing.T) {
		for _, value := range []struct{ input, want string }{{"Guides/Index.mdx", "/guides"}, {"README.md", "/readme"}} {
			if got := routeSlug(value.input); got != value.want {
				t.Errorf("routeSlug(%q) = %q, want %q", value.input, got, value.want)
			}
		}
	})
	t.Run("keeps nested design directories outside the root design corpus", func(t *testing.T) {
		if !IsGatherable("team/docs/designs/page.md") || IsGatherable("docs/designs/page.md") {
			t.Fatal("IsGatherable changed docs/designs membership semantics")
		}
	})
	t.Run("validates source mount and route shape", func(t *testing.T) {
		source := FetchedSource{Source: Source{Repo: "acme/widgets", Mount: "/guide"}}
		if _, err := RemapSourceRoutes([]Page{{Repo: "wrong/repo", Route: "/wrong/repo/guide/a"}}, source); err == nil {
			t.Fatal("accepted wrong repo route")
		}
		if _, err := RemapSourceRoutes([]Page{{Repo: source.Repo, Route: "/acme/widgets/else/a"}}, source); err == nil {
			t.Fatal("accepted route outside mount")
		}
	})
	t.Run("remaps an empty mount and root README", func(t *testing.T) {
		source := FetchedSource{Source: Source{Repo: "acme/widgets", Mount: ""}}
		pages, err := RemapSourceRoutes([]Page{{Repo: source.Repo, SourcePath: "guide.md", Route: "/acme/widgets/guide"}, {Repo: source.Repo, SourcePath: "README.md", Route: "/acme/widgets/readme"}, {Repo: source.Repo, SourcePath: "readme.md", Route: "/acme/widgets/readme"}}, source)
		if err != nil || len(pages) != 3 || pages[0].Route != "/guide" || pages[1].Route != "/" || pages[2].Route != "/" {
			t.Fatalf("RemapSourceRoutes = %#v, %v", pages, err)
		}
	})
	t.Run("rejects duplicate source records", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "sources.json"), []byte(`[{"repo":"acme/widgets","mount":"/widgets/","config":"default"}]`), 0o644); err != nil {
			t.Fatal(err)
		}
		fetched := []FetchedSource{{Source: Source{Repo: "acme/widgets", Mount: "/widgets/", Config: ConfigDefault}}, {Source: Source{Repo: "acme/widgets", Mount: "/widgets/", Config: ConfigDefault}}}
		if err := AssertFetchedSources(root, fetched); err != nil {
			t.Fatal(err)
		}
	})
}
