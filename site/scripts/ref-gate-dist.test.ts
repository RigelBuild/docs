import { afterAll, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { RefGateConfig } from "@rigelbuild/ref-gate";
import { scanDist } from "./ref-gate-dist.ts";

const roots: string[] = [];
const config = {
	prefilter: { ere: "forbidden-marker", ignoreCase: false },
	patterns: [{ source: "forbidden-marker", flags: "" }],
	ignore: [],
	carveOutPaths: [],
	carveOutPrefixes: [],
	allowlist: {},
} satisfies RefGateConfig;

async function fixture(): Promise<string> {
	const root = await mkdtemp(join(tmpdir(), "docs-dist-gate-"));
	roots.push(root);
	return root;
}

afterAll(async () => {
	await Promise.all(
		roots.map((root) => rm(root, { recursive: true, force: true })),
	);
});

describe("built-output reference gate", () => {
	test("finds a forbidden reference in published files", async () => {
		const dist = await fixture();
		await writeFile(join(dist, "index.html"), "published forbidden-marker\n");
		expect(await scanDist(dist, config)).toBe(1);
	});

	test("skips files under pagefind", async () => {
		const dist = await fixture();
		await mkdir(join(dist, "pagefind"), { recursive: true });
		await writeFile(
			join(dist, "pagefind", "pagefind.js"),
			"forbidden-marker\n",
		);
		expect(await scanDist(dist, config)).toBe(0);
	});
});
