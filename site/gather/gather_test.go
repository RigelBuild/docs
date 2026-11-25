// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/RigelBuild/docs/site/gather/internal/bundle"
	"github.com/RigelBuild/docs/site/gather/internal/slug"
	"github.com/RigelBuild/meissa/go/ledger"
)

func TestGatherTransforms(t *testing.T) {
	t.Run("derives title and removes only the first H1", func(t *testing.T) {
		source := "# Project overview\n\nFirst paragraph.\n\n# Later heading\n"
		if got := bundle.ExtractTitle(source, "docs/designs/demo/README.md"); got != "Project overview" {
			t.Fatalf("ExtractTitle = %q", got)
		}
		if got := bundle.StripFirstH1(source); got != "\nFirst paragraph.\n\n# Later heading\n" {
			t.Fatalf("StripFirstH1 = %q", got)
		}
	})
	t.Run("ignores headings inside fenced code", func(t *testing.T) {
		source := "```md\n# Example\n```\n\n# Actual title\n\nBody"
		if got := bundle.ExtractTitle(source, "fallback.md"); got != "Actual title" {
			t.Fatalf("ExtractTitle = %q", got)
		}
		if got := bundle.StripFirstH1(source); got != "```md\n# Example\n```\n\n\nBody" {
			t.Fatalf("StripFirstH1 = %q", got)
		}
	})
	t.Run("routes links to another gathered design", func(t *testing.T) {
		current := "docs/designs/demo/record.md"
		other := "docs/designs/demo/other.md"
		got, err := bundle.Transform("# Record\n\n[Other](other.md)", current, map[string]string{current: "record", other: "other"})
		if err != nil || !strings.Contains(got, "[Other](/designs/demo/other)") {
			t.Fatalf("Transform = %q, %v", got, err)
		}
	})
	t.Run("renders decision pages and decision indexes", func(t *testing.T) {
		decisionPath := "docs/designs/demo/decisions/build/DL-007.md"
		recordPath := "docs/designs/demo/build.md"
		decisionSource := "---\nid: DL-007\ndecision: \"Build by merge\"\nstatus: \"Active (maintainer, 2026-01-01)\"\nrecord: ../../build.md#pipeline\n---\n"
		parsed, err := ledger.ParseDecisionFile(decisionPath, []byte(decisionSource))
		if err != nil {
			t.Fatal(err)
		}
		gathered := map[string]string{decisionPath: decisionSource, recordPath: "# Record"}
		page, err := bundle.Transform(decisionSource, decisionPath, gathered)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`title: "DL-007"`, "**Status:** Active (maintainer, 2026-01-01)", "**Record:** [../../build.md#pipeline](/designs/demo/build#pipeline)"} {
			if !strings.Contains(page, want) {
				t.Fatalf("decision page missing %q: %s", want, page)
			}
		}
		if strings.Contains(page, "id: DL-007") {
			t.Fatalf("decision page retains source frontmatter: %s", page)
		}
		index := bundle.BuildDecisionIndex("demo", []ledger.DecisionRow{parsed}, gathered)
		if !strings.Contains(index, "[DL-007](/designs/demo/decisions/build/dl-007)") {
			t.Fatalf("decision index missing routed link: %s", index)
		}
		if !strings.Contains(index, "## build") {
			t.Fatalf("decision index missing heading: %s", index)
		}
		if strings.Contains(page, "github.com/RigelBuild/docs/blob/main/docs/designs/demo/build.md") {
			t.Fatalf("decision record link fell back to GitHub: %s", page)
		}
	})
	t.Run("preserves query and anchor suffixes", func(t *testing.T) {
		got, err := bundle.Transform("[Overview](README.md?view=raw#intro)\n[Code](../../../site/scripts/gather.ts#L10)\n[Dir](../../../site/)", "docs/designs/demo/record.md", map[string]string{"docs/designs/demo/README.md": "# README"})
		if err != nil || !strings.Contains(got, "[Overview](/designs/demo/readme?view=raw#intro)") {
			t.Fatalf("Transform = %q, %v", got, err)
		}
		for _, want := range []string{"[Code](https://github.com/RigelBuild/docs/blob/main/site/scripts/gather.ts#L10)", "[Dir](https://github.com/RigelBuild/docs/tree/main/site)"} {
			if !strings.Contains(got, want) {
				t.Errorf("Transform missing %q: %s", want, got)
			}
		}
	})
	t.Run("does not rewrite images", func(t *testing.T) {
		got, err := bundle.Transform("![Logo](logo.png)", "docs/designs/demo/record.md", map[string]string{})
		if err != nil || !strings.Contains(got, "![Logo](logo.png)") {
			t.Fatalf("Transform = %q, %v", got, err)
		}
	})
	t.Run("uses Starlight routes and preserves index and MDX behavior", func(t *testing.T) {
		for _, test := range []struct{ input, want string }{
			{"designs/demo/decisions/build/DL-007.md", "/designs/demo/decisions/build/dl-007"},
			{"Guides/Index.mdx", "/guides"},
			{"Guides/Hello, World!.md", "/guides/hello-world"},
		} {
			if got := bundle.RouteSlug(test.input); got != test.want {
				t.Errorf("RouteSlug(%q) = %q, want %q", test.input, got, test.want)
			}
		}
	})
	t.Run("extracts titles using raw-line heading and filename rules", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			source string
			file   string
			want   string
		}{
			{name: "indented heading", source: "    # Code\n", file: "fallback.md", want: "Fallback"},
			{name: "tab heading", source: "#\tTabbed heading\n", file: "fallback.md", want: "Tabbed heading"},
			{name: "fence with info string close", source: "```md\n# Hidden\n``` info\n# Visible\n", file: "fallback.md", want: "Fallback"},
			{name: "unicode filename", file: "école.md", want: "École"},
			{name: "empty filename stem", file: "-.md", want: "Untitled"},
		} {
			t.Run(test.name, func(t *testing.T) {
				if got := bundle.ExtractTitle(test.source, test.file); got != test.want {
					t.Errorf("ExtractTitle(%q) = %q, want %q", test.file, got, test.want)
				}
			})
		}
	})
	t.Run("uses valid source frontmatter title and warns on invalid YAML", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		body, fields := bundle.StripSourceFrontmatter("---\ntitle: Document Title\n---\nBody without a heading", "docs/file.md", logger)
		if got := bundle.PageTitle(body, fields, "docs/file.md"); got != "Document Title" {
			t.Fatalf("PageTitle = %q", got)
		}
		badBody, badFields := bundle.StripSourceFrontmatter("---\ndescription: |\nno indent\n---\n# Fallback", "bad.md", logger)
		if badFields != nil || !strings.HasPrefix(badBody, "# Fallback") {
			t.Fatalf("invalid frontmatter result = %q, %#v", badBody, badFields)
		}
	})
}

func TestGithubSlug(t *testing.T) {
	if got := slug.GithubSlug("Hello World"); got != "hello-world" {
		t.Fatalf("GithubSlug = %q", got)
	}
}
