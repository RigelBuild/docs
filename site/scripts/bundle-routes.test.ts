import { describe, expect, test } from "bun:test";
import type { BundlePage, FetchedSource } from "@rigelbuild/docs-bundle";
import { remapSourceRoutes } from "./bundle-routes.ts";

const source: FetchedSource = {
	repo: "RigelBuild/meissa",
	mount: "/meissa/",
	config: "default",
	root: "/tmp/meissa",
	sha: "a".repeat(40),
};

function page(sourcePath: string, route: string): BundlePage {
	return {
		repo: source.repo,
		sourcePath,
		route,
		section: null,
		pagefind: true,
	};
}

describe("bundled route adaptation", () => {
	test("remaps nested pages to their public repo mount", () => {
		expect(
			remapSourceRoutes(
				[
					page(
						"docs/guides/Setup Guide.md",
						"/RigelBuild/meissa/meissa/docs/guides/setup-guide",
					),
				],
				source,
			),
		).toEqual([
			page("docs/guides/Setup Guide.md", "/meissa/docs/guides/setup-guide"),
		]);
	});

	test("keeps a README page at the mounted repository's index", () => {
		expect(
			remapSourceRoutes(
				[page("README.md", "/RigelBuild/meissa/meissa/readme")],
				source,
			),
		).toEqual([page("README.md", "/meissa")]);
	});
});
