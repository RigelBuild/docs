import { describe, expect, test } from "bun:test";
import {
	buildCloneArgs,
	buildGitEnvironment,
	parsePins,
	sparsePatterns,
} from "./fetch.ts";

describe("source fetch command construction", () => {
	test("uses a shallow filtered main clone for unpinned sources", () => {
		const args = buildCloneArgs("RigelBuild/meissa", "/tmp/source");
		expect(args).toEqual([
			"-c",
			"credential.helper=",
			"clone",
			"--branch",
			"main",
			"--filter=blob:none",
			"--sparse",
			"--depth",
			"1",
			"https://github.com/RigelBuild/meissa.git",
			"/tmp/source",
		]);
	});

	test("keeps blob filtering and drops clone depth for a pinned source", () => {
		const args = buildCloneArgs(
			"RigelBuild/meissa",
			"/tmp/source",
			"a".repeat(40),
		);
		expect(args).toContain("--filter=blob:none");
		expect(args).not.toContain("--depth");
		expect(sparsePatterns()).toContain("/docs-bundle.toml");
		expect(sparsePatterns()).toContain("/**/*.md");
		expect(sparsePatterns()).toContain("/**/*.SVG");
	});

	test("constructs a prompt-free environment without inherited credentials", () => {
		expect(
			buildGitEnvironment({
				PATH: "/usr/bin",
				HOME: "/home/test",
				GH_TOKEN: "not-forwarded",
				GITHUB_TOKEN: "not-forwarded",
				GIT_CONFIG_GLOBAL: "/tmp/hostile-config",
			}),
		).toEqual({
			PATH: "/usr/bin",
			HOME: "/home/test",
			GIT_TERMINAL_PROMPT: "0",
			GIT_CONFIG_NOSYSTEM: "1",
		});
	});

	test("parses source pins and rejects ambiguous entries", () => {
		expect(parsePins(`RigelBuild/meissa=${"b".repeat(40)}`)).toEqual(
			new Map([["RigelBuild/meissa", "b".repeat(40)]]),
		);
		expect(() => parsePins("RigelBuild/meissa=bad")).toThrow("invalid pin");
		expect(() =>
			parsePins(
				`RigelBuild/meissa=${"b".repeat(40)},RigelBuild/meissa=${"c".repeat(40)}`,
			),
		).toThrow("duplicate pin");
	});
});
