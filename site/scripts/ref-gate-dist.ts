import { readdir, readFile } from "node:fs/promises";
import { join, relative, sep } from "node:path";
import {
	type Deps,
	loadRefGateConfig,
	type RefGateConfig,
	runOnce,
} from "@rigelbuild/ref-gate";

async function publishedFiles(distDir: string): Promise<string[]> {
	const files: string[] = [];
	async function walk(directory: string): Promise<void> {
		for (const entry of await readdir(directory, { withFileTypes: true })) {
			if (entry.name === "pagefind" && directory === distDir) continue;
			const path = join(directory, entry.name);
			if (entry.isDirectory()) await walk(path);
			else if (entry.isFile()) files.push(path);
		}
	}
	await walk(distDir);
	return files;
}

async function grepDist(
	distDir: string,
	prefilter: RefGateConfig["prefilter"],
): Promise<string[]> {
	const expression = new RegExp(prefilter.ere, prefilter.ignoreCase ? "i" : "");
	const hits: string[] = [];
	for (const file of await publishedFiles(distDir)) {
		const lines = (await readFile(file, "utf8")).split(/\r?\n/);
		for (const [index, line] of lines.entries()) {
			if (expression.test(line))
				hits.push(
					`${relative(distDir, file).split(sep).join("/")}\0${index + 1}\0${line}`,
				);
			expression.lastIndex = 0;
		}
	}
	return hits;
}

export async function scanDist(
	distDir: string,
	config: RefGateConfig,
): Promise<number> {
	const deps: Deps = {
		grep: (prefilter) => grepDist(distDir, prefilter),
		log: (message) => console.log(message),
		err: (message) => console.error(message),
	};
	return runOnce(deps, config);
}

async function main(): Promise<void> {
	const repoRoot = join(import.meta.dir, "../..");
	const config = loadRefGateConfig(join(repoRoot, "ref-gate.config.json"));
	process.exitCode = await scanDist(join(import.meta.dir, "../dist"), config);
}

if (import.meta.main) await main();
