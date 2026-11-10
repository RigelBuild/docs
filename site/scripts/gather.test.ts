import { describe, expect, test } from "bun:test";
import { parseDecisionFile } from "@rigelbuild/design-ledger-gate/decision-files.ts";
import { extractTitle, routeSlug, stripFirstH1 } from "@rigelbuild/docs-bundle";
import { buildDecisionIndex, transform } from "./gather.ts";

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
});
