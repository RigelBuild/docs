#!/usr/bin/env bun
import { readFileSync, realpathSync } from "node:fs";
import { resolve } from "node:path";

export interface RefGateConfig {
	readonly prefilter: {
		readonly ere: string;
		readonly ignoreCase: boolean;
	};
	readonly patterns: readonly {
		readonly source: string;
		readonly flags: string;
	}[];
	readonly ignore: readonly string[];
	readonly carveOutPaths: readonly string[];
	readonly carveOutPrefixes: readonly string[];
	readonly allowlist: Readonly<Record<string, string>>;
	readonly remediationDoc?: string;
}

export interface Reference {
	readonly file: string;
	readonly line: number;
	readonly text: string;
}

export interface Deps {
	readonly grep: (prefilter: RefGateConfig["prefilter"]) => Promise<string[]>;
	readonly log: (message: string) => void;
	readonly err: (message: string) => void;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

function asRecord(value: unknown, label: string): Record<string, unknown> {
	if (!isRecord(value)) throw new Error(`${label} must be an object`);
	return value;
}

function rejectUnknownKeys(
	record: Record<string, unknown>,
	allowed: readonly string[],
	label: string,
): void {
	for (const key of Object.keys(record)) {
		if (!allowed.includes(key))
			throw new Error(`Unknown key "${key}" in ${label}`);
	}
}

function isUnknownArray(value: unknown): value is readonly unknown[] {
	return Array.isArray(value);
}

function readStringArray(value: unknown, label: string): string[] {
	if (!isUnknownArray(value))
		throw new Error(`${label} must be an array of strings`);
	const result: string[] = [];
	for (const [index, item] of value.entries()) {
		if (typeof item !== "string")
			throw new Error(`${label}[${index}] must be a string`);
		result.push(item);
	}
	return result;
}

export function loadRefGateConfig(path: string): RefGateConfig {
	let parsed: unknown;
	try {
		parsed = JSON.parse(readFileSync(path, "utf8"));
	} catch (error) {
		throw new Error(`Cannot load config "${path}": ${errorMessage(error)}`, {
			cause: error,
		});
	}

	const config = asRecord(parsed, "Config");
	rejectUnknownKeys(
		config,
		[
			"prefilter",
			"patterns",
			"ignore",
			"carveOutPaths",
			"carveOutPrefixes",
			"allowlist",
			"remediationDoc",
		],
		"config",
	);

	const prefilter = asRecord(config.prefilter, "prefilter");
	rejectUnknownKeys(prefilter, ["ere", "ignoreCase"], "prefilter");
	if (typeof prefilter.ere !== "string")
		throw new Error("prefilter.ere must be a string");
	if (typeof prefilter.ignoreCase !== "boolean")
		throw new Error("prefilter.ignoreCase must be a boolean");

	if (!isUnknownArray(config.patterns) || config.patterns.length === 0)
		throw new Error("patterns must be a non-empty array of objects");
	const patterns = config.patterns.map((value, index) => {
		const pattern = asRecord(value, `patterns[${index}]`);
		rejectUnknownKeys(pattern, ["source", "flags"], `patterns[${index}]`);
		if (typeof pattern.source !== "string")
			throw new Error(`patterns[${index}].source must be a string`);
		if (typeof pattern.flags !== "string")
			throw new Error(`patterns[${index}].flags must be a string`);
		try {
			new RegExp(pattern.source, pattern.flags);
		} catch (error) {
			throw new Error(
				`Invalid regular expression in patterns[${index}]: ${errorMessage(error)}`,
				{ cause: error },
			);
		}
		return { source: pattern.source, flags: pattern.flags };
	});

	const ignore = readStringArray(config.ignore, "ignore");
	const carveOutPaths = readStringArray(config.carveOutPaths, "carveOutPaths");
	const carveOutPrefixes = readStringArray(
		config.carveOutPrefixes,
		"carveOutPrefixes",
	);
	const rawAllowlist = asRecord(config.allowlist, "allowlist");
	const allowlistEntries: [string, string][] = [];
	for (const [file, reason] of Object.entries(rawAllowlist)) {
		if (typeof reason !== "string")
			throw new Error(`allowlist[${JSON.stringify(file)}] must be a string`);
		allowlistEntries.push([file, reason]);
	}

	const baseConfig = {
		prefilter: { ere: prefilter.ere, ignoreCase: prefilter.ignoreCase },
		patterns,
		ignore,
		carveOutPaths,
		carveOutPrefixes,
		allowlist: Object.fromEntries(allowlistEntries),
	};
	if (config.remediationDoc === undefined) return baseConfig;
	if (typeof config.remediationDoc !== "string")
		throw new Error("remediationDoc must be a string");
	return { ...baseConfig, remediationDoc: config.remediationDoc };
}

function isCarvedOut(config: RefGateConfig, path: string): boolean {
	return (
		Object.hasOwn(config.allowlist, path) ||
		config.carveOutPaths.includes(path) ||
		config.carveOutPrefixes.some((prefix) => path.startsWith(prefix))
	);
}

function parseGrepHit(
	hit: string,
):
	| { readonly file: string; readonly line: number; readonly text: string }
	| undefined {
	const nulPathEnd = hit.indexOf("\0");
	if (nulPathEnd >= 0) {
		const lineEnd = hit.indexOf("\0", nulPathEnd + 1);
		if (lineEnd < 0 || !/^\d+$/.test(hit.slice(nulPathEnd + 1, lineEnd)))
			return undefined;
		return {
			file: hit.slice(0, nulPathEnd),
			line: Number(hit.slice(nulPathEnd + 1, lineEnd)),
			text: hit.slice(lineEnd + 1),
		};
	}
	let pathEnd = hit.indexOf(":");
	while (pathEnd >= 0) {
		const lineEnd = hit.indexOf(":", pathEnd + 1);
		if (lineEnd < 0) return undefined;
		if (/^\d+$/.test(hit.slice(pathEnd + 1, lineEnd)))
			return {
				file: hit.slice(0, pathEnd),
				line: Number(hit.slice(pathEnd + 1, lineEnd)),
				text: hit.slice(lineEnd + 1),
			};
		pathEnd = hit.indexOf(":", pathEnd + 1);
	}
	return undefined;
}

// Records are path NUL line NUL text LF. A path may hold LF but never NUL, and the
// text never holds LF, so reading fields in order cannot split a path.
export function parseGitGrepOutput(output: string): string[] {
	const hits: string[] = [];
	let index = 0;
	while (index < output.length) {
		const pathEnd = output.indexOf("\0", index);
		const lineEnd = pathEnd < 0 ? -1 : output.indexOf("\0", pathEnd + 1);
		const line = lineEnd < 0 ? "" : output.slice(pathEnd + 1, lineEnd);
		if (!/^\d+$/.test(line))
			throw new Error(`unparsable git grep output at offset ${index}`);
		const textEnd = output.indexOf("\n", lineEnd + 1);
		const end = textEnd < 0 ? output.length : textEnd;
		hits.push(
			`${output.slice(index, pathEnd)}\0${line}\0${output.slice(lineEnd + 1, end)}`,
		);
		index = end + 1;
	}
	return hits;
}

function escapeRegExp(value: string): string {
	return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export function findViolations(
	config: RefGateConfig,
	grepHits: readonly string[],
): Reference[] {
	const patterns = config.patterns.map(
		({ source, flags }) => new RegExp(source, flags),
	);
	const ignoredNames = config.ignore
		.filter((name) => name.length > 0)
		.map((name) => new RegExp(escapeRegExp(name), "gi"));
	const violations: Reference[] = [];

	for (const hit of grepHits) {
		const line = hit.replace(/[\r\n]+$/, "");
		if (line.length === 0) continue;
		const parsed = parseGrepHit(line);
		if (!parsed || !Number.isSafeInteger(parsed.line))
			throw new Error(`unparsable grep hit: ${JSON.stringify(line)}`);
		const { file, line: lineNumber, text } = parsed;
		if (isCarvedOut(config, file)) continue;
		let matchText = text;
		for (const ignoredName of ignoredNames)
			matchText = matchText.replace(ignoredName, "");
		if (
			patterns.some((pattern) => {
				pattern.lastIndex = 0;
				return pattern.test(matchText);
			})
		) {
			violations.push({ file, line: lineNumber, text });
		}
	}
	return violations;
}

export async function runOnce(
	deps: Deps,
	config: RefGateConfig,
): Promise<number> {
	try {
		const hits = await deps.grep(config.prefilter);
		const violations = findViolations(config, hits);
		if (violations.length === 0) {
			deps.log("ref-gate: clean");
			return 0;
		}
		for (const violation of violations) {
			deps.err(`${violation.file}:${violation.line}: ${violation.text}`);
		}
		if (config.remediationDoc) deps.err(`See ${config.remediationDoc}.`);
		return 1;
	} catch (error) {
		deps.err(`ref-gate: scan failed: ${errorMessage(error)}`);
		return 2;
	}
}

export function gitGrep(
	root: string,
	prefilter: RefGateConfig["prefilter"],
): string[] {
	// A root nested in another repo would scan the enclosing tree and pass falsely.
	const top = Bun.spawnSync({
		cmd: ["git", "-C", root, "rev-parse", "--show-toplevel"],
		stdout: "pipe",
		stderr: "pipe",
	});
	const topLevel = new TextDecoder().decode(top.stdout).trim();
	if (top.exitCode !== 0 || realpathSync(topLevel) !== realpathSync(root)) {
		throw new Error(`${root} is not the top level of a git repository`);
	}
	const args = [
		"git",
		"-C",
		root,
		"grep",
		"--no-color",
		"--null",
		"-n",
		// Text mode on every file: skipping "binary" ones would let a NUL byte or a
		// .gitattributes mark hide a file. Carve out real binaries by path instead.
		"-a",
		"-E",
	];
	if (prefilter.ignoreCase) args.push("-i");
	args.push("-e", prefilter.ere);
	const result = Bun.spawnSync({ cmd: args, stdout: "pipe", stderr: "pipe" });
	if (result.exitCode === 1) return [];
	if (result.exitCode !== 0) {
		const detail = new TextDecoder().decode(result.stderr).trim();
		throw new Error(`git grep exited ${result.exitCode}: ${detail}`);
	}
	return parseGitGrepOutput(new TextDecoder().decode(result.stdout));
}

function scanRoot(): string {
	const configuredRoot = process.env.GATE_ROOT;
	if (configuredRoot !== undefined) return resolve(configuredRoot);
	const result = Bun.spawnSync({
		cmd: ["git", "rev-parse", "--show-toplevel"],
		stdout: "pipe",
		stderr: "pipe",
	});
	if (result.exitCode !== 0) {
		const detail = new TextDecoder().decode(result.stderr).trim();
		throw new Error(`cannot find git root: ${detail}`);
	}
	return new TextDecoder().decode(result.stdout).trim();
}

function configArgument(args: readonly string[]): string | undefined {
	if (args.length !== 2 || args[0] !== "--config" || !args[1]) return undefined;
	return args[1];
}

async function main(args: readonly string[]): Promise<number> {
	const configPath = configArgument(args);
	if (!configPath) {
		console.error("Usage: ref-gate --config <path>");
		return 2;
	}
	try {
		const config = loadRefGateConfig(configPath);
		const root = scanRoot();
		return await runOnce(
			{
				grep: async (prefilter) => gitGrep(root, prefilter),
				log: (message) => console.log(message),
				err: (message) => console.error(message),
			},
			config,
		);
	} catch (error) {
		console.error(`ref-gate: ${errorMessage(error)}`);
		return 2;
	}
}

if (import.meta.main) process.exitCode = await main(process.argv.slice(2));
