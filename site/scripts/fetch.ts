import { spawn } from "node:child_process";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { basename, join } from "node:path";

interface Source {
	repo: string;
	mount: string;
	config: "required" | "default";
}

interface FetchedSource extends Source {
	root: string;
	sha: string;
}

interface FetchOptions {
	sources: readonly Source[];
	workDir: string;
	pins: ReadonlyMap<string, string>;
	production: boolean;
	/** Clone URL prefix. Defaults to GitHub. */
	baseUrl?: string;
}

const REPO_PATTERN = /^RigelBuild\/[A-Za-z0-9_-][A-Za-z0-9_.-]*$/;
const GITHUB_BASE_URL = "https://github.com/";

const IMAGE_EXTENSIONS = [
	"png",
	"jpg",
	"jpeg",
	"gif",
	"svg",
	"webp",
	"avif",
] as const;

export function parsePins(value: string | undefined): Map<string, string> {
	const pins = new Map<string, string>();
	if (value === undefined || value.trim() === "") return pins;
	for (const entry of value.split(",")) {
		const [repo, sha, ...extra] = entry.trim().split("=");
		if (
			repo === undefined ||
			!REPO_PATTERN.test(repo) ||
			sha === undefined ||
			!/^[a-f\d]{40}$/i.test(sha) ||
			extra.length > 0
		) {
			throw new Error(`invalid pin: ${entry}`);
		}
		if (pins.has(repo)) throw new Error(`duplicate pin: ${repo}`);
		pins.set(repo, sha);
	}
	return pins;
}

export function sparsePatterns(): string[] {
	return [
		"/*.md",
		"/**/*.md",
		...IMAGE_EXTENSIONS.flatMap((extension) => [
			`/*.${extension}`,
			`/**/*.${extension}`,
			`/*.${extension.toUpperCase()}`,
			`/**/*.${extension.toUpperCase()}`,
		]),
		"/docs-bundle.toml",
	];
}

export function buildCloneArgs(
	repo: string,
	destination: string,
	pin?: string,
	baseUrl = GITHUB_BASE_URL,
): string[] {
	const args = [
		"-c",
		"credential.helper=",
		"clone",
		"--branch",
		"main",
		"--filter=blob:none",
		"--sparse",
	];
	if (pin === undefined) args.push("--depth", "1");
	const prefix = baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`;
	args.push(`${prefix}${repo}.git`, destination);
	return args;
}

export function buildGitEnvironment(
	environment: NodeJS.ProcessEnv = process.env,
): NodeJS.ProcessEnv {
	const safe: NodeJS.ProcessEnv = {
		GIT_TERMINAL_PROMPT: "0",
		GIT_CONFIG_NOSYSTEM: "1",
		GIT_CONFIG_GLOBAL: "/dev/null",
	};
	for (const name of [
		"PATH",
		"HOME",
		"TMPDIR",
		"TEMP",
		"TMP",
		"SystemRoot",
		"WINDIR",
	]) {
		const value = environment[name];
		if (value !== undefined) safe[name] = value;
	}
	return safe;
}

// First argument after any leading `-C <dir>` or `-c <kv>` pairs.
function gitSubcommand(args: readonly string[]): string | undefined {
	let index = 0;
	while (args[index] === "-C" || args[index] === "-c") index += 2;
	return args[index];
}

function runGit(
	args: readonly string[],
	cwd: string,
	env: NodeJS.ProcessEnv,
): Promise<string> {
	return new Promise<string>((resolve, reject) => {
		const child = spawn("git", [...args], {
			cwd,
			env,
			stdio: ["ignore", "pipe", "pipe"],
		});
		let stdout = "";
		let stderr = "";
		child.stdout.setEncoding("utf8");
		child.stderr.setEncoding("utf8");
		child.stdout.on("data", (chunk: string) => {
			stdout += chunk;
		});
		child.stderr.on("data", (chunk: string) => {
			stderr += chunk;
		});
		child.on("error", reject);
		child.on("close", (code) => {
			if (code === 0) resolve(stdout.trim());
			else
				reject(
					new Error(
						`git ${gitSubcommand(args)} failed (${code}): ${stderr.trim()}`,
					),
				);
		});
	});
}

async function fetchSource(
	source: Source,
	workDir: string,
	pin: string | undefined,
	production: boolean,
	baseUrl: string,
	env: NodeJS.ProcessEnv,
): Promise<FetchedSource> {
	const root = join(workDir, "sources", basename(source.repo));
	await rm(root, { recursive: true, force: true });
	await mkdir(join(workDir, "sources"), { recursive: true });
	await runGit(buildCloneArgs(source.repo, root, pin, baseUrl), workDir, env);
	await runGit(
		["-C", root, "sparse-checkout", "set", "--no-cone", ...sparsePatterns()],
		workDir,
		env,
	);
	if (pin !== undefined) {
		if (production) {
			await runGit(
				["-C", root, "merge-base", "--is-ancestor", pin, "origin/main"],
				workDir,
				env,
			);
		}
		await runGit(["-C", root, "checkout", "--detach", pin], workDir, env);
	}
	const sha = await runGit(["-C", root, "rev-parse", "HEAD"], workDir, env);
	await rm(join(root, ".git"), { recursive: true, force: true });
	return {
		...source,
		mount: source.mount.replace(/^\/+|\/+$/g, ""),
		root,
		sha,
	};
}

export function isSource(value: unknown): value is Source {
	if (typeof value !== "object" || value === null || Array.isArray(value))
		return false;
	const record = value as Record<string, unknown>;
	return (
		typeof record.repo === "string" &&
		REPO_PATTERN.test(record.repo) &&
		typeof record.mount === "string" &&
		(record.config === "default" || record.config === "required")
	);
}

async function readSources(path: string): Promise<Source[]> {
	const parsed: unknown = JSON.parse(await readFile(path, "utf8"));
	if (!Array.isArray(parsed) || !parsed.every(isSource))
		throw new Error(`invalid bundle source list: ${path}`);
	return parsed;
}

export async function fetchSources(options: FetchOptions): Promise<void> {
	await mkdir(options.workDir, { recursive: true });
	const fetchedPath = join(options.workDir, "fetched.json");
	await rm(fetchedPath, { force: true });
	const env = buildGitEnvironment();
	const baseUrl = options.baseUrl ?? GITHUB_BASE_URL;
	for (const repo of options.pins.keys()) {
		if (!options.sources.some((source) => source.repo === repo))
			throw new Error(`pin does not match a configured source: ${repo}`);
	}
	const fetched: FetchedSource[] = [];
	for (const source of options.sources) {
		fetched.push(
			await fetchSource(
				source,
				options.workDir,
				options.pins.get(source.repo),
				options.production,
				baseUrl,
				env,
			),
		);
	}
	await writeFile(fetchedPath, `${JSON.stringify(fetched, null, 2)}\n`);
}

async function main(): Promise<void> {
	const siteRoot = join(import.meta.dir, "..");
	const workDir = join(siteRoot, ".bundle");
	const sources = await readSources(join(siteRoot, "sources.json"));
	await mkdir(workDir, { recursive: true });
	await fetchSources({
		sources,
		workDir,
		pins: parsePins(process.env.DOCS_BUNDLE_PINS),
		production: ["push", "schedule", "workflow_dispatch"].includes(
			process.env.GITHUB_EVENT_NAME ?? "",
		),
	});
}

if (import.meta.main) await main();
