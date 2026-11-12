import { afterEach, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { parseDecisionFile } from "@rigelbuild/design-ledger-gate/decision-files.ts";
import {
	extractTitle,
	type FetchedSource,
	routeSlug,
	stripFirstH1,
} from "@rigelbuild/docs-bundle";
import { buildDecisionIndex, gather, transform } from "./gather.ts";

const cleanups: string[] = [];

afterEach(async () => {
	await Promise.all(
		cleanups
			.splice(0)
			.map((path) => rm(path, { recursive: true, force: true })),
	);
});

interface Fixture {
	repoRoot: string;
	siteRoot: string;
	source: FetchedSource;
}

async function makeFixture(
	pages: Record<string, string>,
	configured: readonly string[] = ["RigelBuild/tool"],
	mount = "tool",
): Promise<Fixture> {
	const base = await mkdtemp(join(tmpdir(), "gather-test-"));
	cleanups.push(base);
	const repoRoot = join(base, "repo");
	const siteRoot = join(repoRoot, "site");
	const sourceRoot = join(base, "source");
	await mkdir(join(repoRoot, "docs", "designs", "demo"), { recursive: true });
	await writeFile(
		join(repoRoot, "docs", "designs", "demo", "record.md"),
		"# Record\n\nBody.\n",
	);
	await mkdir(siteRoot, { recursive: true });
	await writeFile(
		join(siteRoot, "sources.json"),
		JSON.stringify(
			configured.map((repo) => ({
				repo,
				mount: `/${mount}/`,
				config: "default",
			})),
		),
	);
	for (const [path, content] of Object.entries(pages)) {
		await mkdir(dirname(join(sourceRoot, path)), { recursive: true });
		await writeFile(join(sourceRoot, path), content);
	}
	return {
		repoRoot,
		siteRoot,
		source: {
			repo: "RigelBuild/tool",
			mount,
			config: "default",
			root: sourceRoot,
			sha: "a".repeat(40),
		},
	};
}

describe("gather transforms", () => {
	test("derives a title from the first H1 and removes only that H1", () => {
		const source =
			"# Project overview\n\nFirst paragraph.\n\n# Later heading\n";
		expect(extractTitle(source, "docs/designs/demo/README.md")).toBe(
			"Project overview",
		);
		expect(stripFirstH1(source)).toBe(
			"\nFirst paragraph.\n\n# Later heading\n",
		);
	});
	test("ignores H1 text inside fenced code", () => {
		const source = "```md\n# Example\n```\n\n# Actual title\n\nBody";
		expect(extractTitle(source, "docs/designs/demo/README.md")).toBe(
			"Actual title",
		);
		expect(stripFirstH1(source)).toBe("```md\n# Example\n```\n\n\nBody");
	});

	test("rewrites gathered Markdown links to their site route", () => {
		const path = "docs/designs/demo/record.md";
		const source = "# Record\n\n[Overview](README.md#intro)";
		const gathered = new Set([path, "docs/designs/demo/README.md"]);
		expect(transform(source, path, gathered)).toContain(
			"[Overview](/designs/demo/readme#intro)",
		);
	});

	test("renders decision pages and per-area indexes from shared parser rows", () => {
		const decisionPath = "docs/designs/demo/decisions/build/DL-007.md";
		const recordPath = "docs/designs/demo/build.md";
		const decisionSource =
			'---\nid: DL-007\ndecision: "Build by merge"\nstatus: "Active (maintainer, 2026-01-01)"\nrecord: ../../build.md#pipeline\n---\n';
		const parsed = parseDecisionFile(decisionPath, decisionSource);
		if (!parsed.ok) throw new Error("fixture must parse");
		const gathered = new Set([decisionPath, recordPath]);
		const page = transform(decisionSource, decisionPath, gathered);
		expect(page).toContain('title: "DL-007"');
		expect(page).toContain("**Status:** Active (maintainer, 2026-01-01)");
		expect(page).toContain(
			"**Record:** [../../build.md#pipeline](/designs/demo/build#pipeline)",
		);
		expect(page).not.toContain("id: DL-007");
		const index = buildDecisionIndex("demo", [parsed.row], gathered);
		expect(index).toContain("## build");
		expect(index).toContain("[DL-007](/designs/demo/decisions/build/dl-007)");
	});

	test("uses the Starlight route slug for paths", () => {
		expect(routeSlug("designs/demo/decisions/build/DL-007.md")).toBe(
			"/designs/demo/decisions/build/dl-007",
		);
	});

	test("keeps query and anchor suffixes on routed and fallback links", () => {
		const path = "docs/designs/demo/record.md";
		const gathered = new Set([path, "docs/designs/demo/README.md"]);
		const source =
			"# Record\n\n[Overview](README.md?view=raw#intro) [Code](../../../site/scripts/gather.ts#L10) [Dir](../../../site/)";
		const page = transform(source, path, gathered);
		expect(page).toContain("[Overview](/designs/demo/readme?view=raw#intro)");
		expect(page).toContain(
			"[Code](https://github.com/RigelBuild/docs/blob/main/site/scripts/gather.ts#L10)",
		);
		expect(page).toContain(
			"[Dir](https://github.com/RigelBuild/docs/tree/main/site)",
		);
	});

	test("leaves image links unchanged", () => {
		const path = "docs/designs/demo/record.md";
		const page = transform(
			"# Record\n\n![Logo](logo.png)",
			path,
			new Set([path]),
		);
		expect(page).toContain("![Logo](logo.png)");
	});
});

describe("gather over a source tree", () => {
	test("writes bundled pages with editUrl frontmatter and counts them in the manifest", async () => {
		const fixture = await makeFixture({
			"guide.md": "# Guide\n\nBody.\n",
			"README.md": "# Tool\n\nIntro.\n",
		});
		const manifest = await gather({
			repoRoot: fixture.repoRoot,
			siteRoot: fixture.siteRoot,
			sources: [fixture.source],
		});
		const content = join(fixture.siteRoot, "src", "content", "docs");
		const guide = await readFile(
			join(content, "tool", "guide", "index.md"),
			"utf8",
		);
		expect(guide).toContain(
			'editUrl: "https://github.com/RigelBuild/tool/edit/main/guide.md"',
		);
		const readme = await readFile(join(content, "tool", "index.md"), "utf8");
		expect(readme).toContain(
			'editUrl: "https://github.com/RigelBuild/tool/edit/main/README.md"',
		);
		expect(manifest.sources).toEqual([
			{ repo: "RigelBuild/tool", ref: "main", sha: "a".repeat(40), pages: 2 },
		]);
		const written: unknown = JSON.parse(
			await readFile(join(fixture.siteRoot, "public", "_bundle.json"), "utf8"),
		);
		expect(written).toMatchObject({ sources: manifest.sources });
	});

	test("strips source frontmatter and titles the page from its H1 or page_title", async () => {
		const fixture = await makeFixture({
			"guide.md":
				'---\n# generated by https://github.com/hashicorp/terraform-plugin-docs\npage_title: "user Resource"\n---\n# user Resource\n\nBody text.\n',
			"fallback.md":
				'---\npage_title: "Setup Guide"\n---\n\nBody without a heading.\n',
		});
		await gather({
			repoRoot: fixture.repoRoot,
			siteRoot: fixture.siteRoot,
			sources: [fixture.source],
		});
		const content = join(fixture.siteRoot, "src", "content", "docs");
		const guide = await readFile(
			join(content, "tool", "guide", "index.md"),
			"utf8",
		);
		expect(guide).toContain('title: "user Resource"');
		expect(guide).not.toContain("generated by");
		expect(guide).not.toContain("page_title");
		expect(guide.match(/^---$/gm)).toHaveLength(2);
		const fallback = await readFile(
			join(content, "tool", "fallback", "index.md"),
			"utf8",
		);
		expect(fallback).toContain('title: "Setup Guide"');
		expect(fallback).not.toContain("page_title");
	});

	test("strips frontmatter that is not valid YAML instead of failing", async () => {
		const fixture = await makeFixture({
			"guide.md":
				'---\nlayout: ""\ndescription: |-\nUnindented block text\n---\n# Upgrade Guide\n\nBody.\n',
		});
		await gather({
			repoRoot: fixture.repoRoot,
			siteRoot: fixture.siteRoot,
			sources: [fixture.source],
		});
		const guide = await readFile(
			join(
				fixture.siteRoot,
				"src",
				"content",
				"docs",
				"tool",
				"guide",
				"index.md",
			),
			"utf8",
		);
		expect(guide).toContain('title: "Upgrade Guide"');
		expect(guide).not.toContain("Unindented block text");
	});

	test("refuses to gather when a configured source was not fetched", async () => {
		const fixture = await makeFixture({ "guide.md": "# Guide\n" }, [
			"RigelBuild/tool",
			"RigelBuild/other",
		]);
		await expect(
			gather({
				repoRoot: fixture.repoRoot,
				siteRoot: fixture.siteRoot,
				sources: [fixture.source],
			}),
		).rejects.toThrow("missing fetched source(s): RigelBuild/other");
	});

	test("refuses a source mounted at designs", async () => {
		const fixture = await makeFixture(
			{ "guide.md": "# Guide\n" },
			undefined,
			"designs",
		);
		await expect(
			gather({
				repoRoot: fixture.repoRoot,
				siteRoot: fixture.siteRoot,
				sources: [fixture.source],
			}),
		).rejects.toThrow("source mount may not be designs: RigelBuild/tool");
	});
});
