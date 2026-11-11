import { afterEach, describe, expect, test } from "bun:test";
import { execFile } from "node:child_process";
import {
	mkdir,
	mkdtemp,
	readFile,
	rm,
	stat,
	writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";
import {
	buildCloneArgs,
	buildGitEnvironment,
	fetchSources,
	isSource,
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
			GIT_CONFIG_GLOBAL: "/dev/null",
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

	test("rejects dot-named repositories in pins and source lists", () => {
		for (const repo of [
			"RigelBuild/.",
			"RigelBuild/..",
			"RigelBuild/.hidden",
		]) {
			expect(() => parsePins(`${repo}=${"b".repeat(40)}`)).toThrow(
				"invalid pin",
			);
			expect(isSource({ repo, mount: "tool", config: "default" })).toBe(false);
		}
		expect(
			isSource({
				repo: "RigelBuild/jj-vine",
				mount: "/jj-vine/",
				config: "default",
			}),
		).toBe(true);
	});
});

const REPO = "RigelBuild/tool";
const execFileAsync = promisify(execFile);
const cleanups: string[] = [];

afterEach(async () => {
	await Promise.all(
		cleanups
			.splice(0)
			.map((path) => rm(path, { recursive: true, force: true })),
	);
});

interface Remote {
	baseUrl: string;
	first: string;
	second: string;
	side: string;
}

async function git(args: readonly string[], cwd: string): Promise<string> {
	const { stdout } = await execFileAsync("git", [...args], {
		cwd,
		env: {
			...buildGitEnvironment(),
			GIT_AUTHOR_NAME: "Fetch Test",
			GIT_AUTHOR_EMAIL: "fetch-test@example.invalid",
			GIT_COMMITTER_NAME: "Fetch Test",
			GIT_COMMITTER_EMAIL: "fetch-test@example.invalid",
		},
		encoding: "utf8",
	});
	return stdout.trim();
}

async function commitAll(cwd: string, message: string): Promise<void> {
	await git(["add", "--all"], cwd);
	await git(["commit", "--quiet", "-m", message], cwd);
}

// Builds RigelBuild/tool.git under a temp dir: main has two commits, and
// `side` branches off the first commit, so its SHA is not on main.
async function makeRemote(): Promise<Remote> {
	const base = await mkdtemp(join(tmpdir(), "fetch-test-"));
	cleanups.push(base);
	const bare = join(base, "RigelBuild", "tool.git");
	const scratch = join(base, "scratch");
	await mkdir(join(base, "RigelBuild"), { recursive: true });
	await git(["init", "--quiet", "--bare", bare], base);
	await git(["symbolic-ref", "HEAD", "refs/heads/main"], bare);
	// Without this the server drops --filter and the blob:none path is untested.
	await git(["config", "uploadpack.allowFilter", "true"], bare);

	await git(["init", "--quiet", scratch], base);
	await git(["symbolic-ref", "HEAD", "refs/heads/main"], scratch);
	await git(["remote", "add", "origin", bare], scratch);
	await writeFile(join(scratch, "README.md"), "# one\n");
	await writeFile(join(scratch, "docs-bundle.toml"), 'name = "tool"\n');
	await commitAll(scratch, "first");
	const first = await git(["rev-parse", "HEAD"], scratch);
	await writeFile(join(scratch, "README.md"), "# two\n");
	await commitAll(scratch, "second");
	const second = await git(["rev-parse", "HEAD"], scratch);
	await git(["checkout", "--quiet", "-b", "side", first], scratch);
	await writeFile(join(scratch, "side.md"), "# side\n");
	await commitAll(scratch, "side");
	const side = await git(["rev-parse", "HEAD"], scratch);
	await git(["push", "--quiet", "origin", "main", "side"], scratch);
	// Trailing slash: fetch.ts appends `${repo}.git` directly to baseUrl.
	return { baseUrl: `${pathToFileURL(base).href}/`, first, second, side };
}

async function fetchTool(
	remote: Remote,
	pins: ReadonlyMap<string, string>,
	production: boolean,
): Promise<string> {
	const workDir = await mkdtemp(join(tmpdir(), "fetch-work-"));
	cleanups.push(workDir);
	await fetchSources({
		sources: [{ repo: REPO, mount: "tool", config: "default" }],
		workDir,
		pins,
		production,
		baseUrl: remote.baseUrl,
	});
	return workDir;
}

async function readFetched(workDir: string): Promise<unknown> {
	return JSON.parse(await readFile(join(workDir, "fetched.json"), "utf8"));
}

describe("source fetch against a local bare repo", () => {
	test("checks out the pinned SHA instead of the branch head", async () => {
		const remote = await makeRemote();
		const workDir = await fetchTool(
			remote,
			new Map([[REPO, remote.first]]),
			false,
		);
		expect(
			await readFile(join(workDir, "sources", "tool", "README.md"), "utf8"),
		).toBe("# one\n");
		expect(await readFetched(workDir)).toEqual([
			expect.objectContaining({ repo: REPO, sha: remote.first }),
		]);
	});

	test("production rejects a pin that is not an ancestor of main", async () => {
		const remote = await makeRemote();
		const ancestor = await fetchTool(
			remote,
			new Map([[REPO, remote.first]]),
			true,
		);
		expect(await readFetched(ancestor)).toEqual([
			expect.objectContaining({ sha: remote.first }),
		]);

		// runGit labels failures with args[0], which is "-C" for this call.
		await expect(
			fetchTool(remote, new Map([[REPO, remote.side]]), true),
		).rejects.toThrow("git -C failed (1)");

		// Control: the same side-branch SHA is accepted when production is off.
		const dev = await fetchTool(remote, new Map([[REPO, remote.side]]), false);
		expect(await readFetched(dev)).toEqual([
			expect.objectContaining({ sha: remote.side }),
		]);
	});

	test("removes .git from the fetched tree", async () => {
		const remote = await makeRemote();
		const workDir = await fetchTool(remote, new Map<string, string>(), false);
		const root = join(workDir, "sources", "tool");
		expect(await readFile(join(root, "README.md"), "utf8")).toBe("# two\n");
		await expect(stat(join(root, ".git"))).rejects.toMatchObject({
			code: "ENOENT",
		});
	});

	test("writes fetched.json with repo, sha, and root per source", async () => {
		const remote = await makeRemote();
		const workDir = await fetchTool(remote, new Map<string, string>(), false);
		expect(await readFetched(workDir)).toEqual([
			{
				repo: REPO,
				mount: "tool",
				config: "default",
				root: join(workDir, "sources", "tool"),
				sha: remote.second,
			},
		]);
	});
});
