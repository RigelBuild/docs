import { describe, expect, test } from "bun:test";

function record(value: unknown): Record<string, unknown> {
	if (typeof value !== "object" || value === null || Array.isArray(value)) {
		throw new Error("Expected a YAML mapping");
	}
	const entries: [string, unknown][] = Object.entries(value);
	return Object.fromEntries(entries);
}

function strings(value: unknown): string[] {
	if (
		!Array.isArray(value) ||
		value.some((entry) => typeof entry !== "string")
	) {
		throw new Error("Expected a list of path patterns");
	}
	return value;
}

const workflowPromise = Bun.file(
	new URL("../dispatch/docs-dispatch.yml", import.meta.url),
)
	.text()
	.then((source) => record(Bun.YAML.parse(source)));

function paths(workflow: Record<string, unknown>): string[] {
	const triggers = record(workflow.on);
	const push = record(triggers.push);
	return strings(push.paths);
}

async function matchesPath(path: string): Promise<boolean> {
	const patterns = paths(await workflowPromise);
	if (patterns.some((pattern) => pattern.includes("{"))) {
		throw new Error("Workflow path patterns must not use brace expansion");
	}
	const globs = patterns.map((pattern) => new Bun.Glob(pattern));
	return globs.some((glob) => glob.match(path));
}

describe("canonical docs dispatch workflow", () => {
	test("triggers only on pushes to main", async () => {
		const triggers = record((await workflowPromise).on);
		expect(Object.keys(triggers)).toEqual(["push"]);
		const push = record(triggers.push);
		expect(push.branches).toEqual(["main"]);
		expect(Object.keys(push).sort()).toEqual(["branches", "paths"]);
	});

	test("uses the main environment and empty job permissions", async () => {
		const jobs = record((await workflowPromise).jobs);
		const dispatch = record(jobs.dispatch);
		expect(dispatch.environment).toBe("main");
		expect(dispatch.permissions).toEqual({});
	});

	test("passes the app token to the workflow dispatch command", async () => {
		const jobs = record((await workflowPromise).jobs);
		const dispatch = record(jobs.dispatch);
		if (!Array.isArray(dispatch.steps))
			throw new Error("Expected workflow steps");
		const steps = dispatch.steps.map(record);
		const tokenStep = steps.find((step) => step.id === "app-token");
		expect(tokenStep?.id).toBe("app-token");
		const runStep = steps.find(
			(step) =>
				typeof step.run === "string" && step.run.includes("gh workflow run"),
		);
		expect(record(runStep?.env).GH_TOKEN).toBe(
			`\${{ steps.app-token.outputs.token }}`,
		);
	});

	test("has brace-free path patterns that compile", async () => {
		const patterns = paths(await workflowPromise);
		expect(patterns.every((pattern) => !pattern.includes("{"))).toBe(true);
		expect(() =>
			patterns.map((pattern) => new Bun.Glob(pattern)),
		).not.toThrow();
	});

	test("includes lower- and upper-case image patterns", async () => {
		const patterns = paths(await workflowPromise);
		for (const extension of [
			"png",
			"jpg",
			"jpeg",
			"gif",
			"svg",
			"webp",
			"avif",
		]) {
			expect(patterns).toContain(`**/*.${extension}`);
			expect(patterns).toContain(`**/*.${extension.toUpperCase()}`);
		}
	});

	for (const path of [
		"docs/arch/diagram.png",
		"docs/arch/DIAGRAM.SVG",
		"README.md",
		"docs/guide.md",
		"docs-bundle.toml",
	]) {
		test(`matches ${path}`, async () => {
			expect(await matchesPath(path)).toBe(true);
		});
	}

	for (const path of ["src/main.go", "docs/arch/diagram.bmp"]) {
		test(`does not match ${path}`, async () => {
			expect(await matchesPath(path)).toBe(false);
		});
	}
});
