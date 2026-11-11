import { mkdir, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join, relative } from "node:path";
import {
	type DecisionRow,
	parseDecisionFile,
	renderDecisionIndex,
} from "@rigelbuild/design-ledger-gate/decision-files.ts";
import {
	assertNoRouteCollisions,
	type BundleManifest,
	type BundlePage,
	collectPages,
	extractTitle,
	type FetchedSource,
	readFetched,
	readPublishConfig,
	rewriteLinks as rewriteBundleLinks,
	routeSlug,
	stripFirstH1,
	writeManifest,
} from "@rigelbuild/docs-bundle";
import { YAML } from "bun";
import { remapSourceRoutes } from "./bundle-routes.ts";

const REPO_SLUG = "RigelBuild/docs";
const DECISION_PATH_RE =
	/^docs\/designs\/([^/]+)\/decisions\/[^/]+\/DL-\d+\.md$/;
const SOURCE_FRONTMATTER_RE = /^\uFEFF?---\r?\n([\s\S]*?)\r?\n---\r?\n?/;
const DESIGN_LINK = /(!?\[[^\][\n]*)?(\]\()([^)\s]+)(\s+"[^"]*")?(\))/g;

function frontmatter(title: string, sourcePath?: string): string {
	const escaped = title.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
	const editUrl =
		sourcePath === undefined
			? ""
			: `editUrl: "https://github.com/${REPO_SLUG}/edit/main/${sourcePath}"\n`;
	return `---\ntitle: "${escaped}"\n${editUrl}---\n`;
}

export function transform(
	source: string,
	sourcePath: string,
	gathered: ReadonlySet<string>,
): string {
	if (DECISION_PATH_RE.test(sourcePath)) {
		const parsed = parseDecisionFile(sourcePath, source);
		if (!parsed.ok)
			throw new Error(
				`${sourcePath}:${parsed.error.line}: ${parsed.error.reason}`,
			);
		const { id, decision, status, recordRaw } = parsed.row;
		const body = source.replace(/^\uFEFF?---\r?\n[\s\S]*?\r?\n---\r?\n?/, "");
		const page = `${decision}\n\n**Status:** ${status}\n\n**Record:** [${recordRaw}](${recordRaw})\n${body.trim() === "" ? "" : `\n${body}`}`;
		return (
			frontmatter(id, sourcePath) +
			rewriteDesignLinks(page, sourcePath, gathered)
		);
	}
	return (
		frontmatter(extractTitle(source, sourcePath), sourcePath) +
		rewriteDesignLinks(stripFirstH1(source), sourcePath, gathered)
	);
}

function rewriteDesignLinks(
	source: string,
	sourcePath: string,
	gathered: ReadonlySet<string>,
): string {
	return source.replace(
		DESIGN_LINK,
		(
			match,
			label: string | undefined,
			open: string,
			target: string,
			title: string | undefined,
			close: string,
		) => {
			if (label?.startsWith("!")) return match;
			if (/^(?:[a-z][a-z\d+.-]*:|\/|#)/i.test(target)) return match;
			const suffixAt = target.search(/[?#]/);
			const linkPath = suffixAt < 0 ? target : target.slice(0, suffixAt);
			const suffix = suffixAt < 0 ? "" : target.slice(suffixAt);
			if (linkPath.length === 0) return match;
			const resolved = join(dirname(sourcePath), linkPath).replaceAll(
				"\\",
				"/",
			);
			const normalized = resolved
				.split("/")
				.reduce<string[]>((parts, segment) => {
					if (segment === "..") parts.pop();
					else if (segment !== "." && segment !== "") parts.push(segment);
					return parts;
				}, [])
				.join("/");
			const prefix = `${label ?? ""}${open}`;
			const tail = `${title ?? ""}${close}`;
			if (/\.md$/i.test(normalized) && gathered.has(normalized)) {
				const route = routeSlug(normalized.replace(/^docs\//, ""));
				return `${prefix}${route}${suffix}${tail}`;
			}
			const kind = linkPath.endsWith("/") ? "tree" : "blob";
			return `${prefix}https://github.com/${REPO_SLUG}/${kind}/main/${normalized}${suffix}${tail}`;
		},
	);
}

export function buildDecisionIndex(
	surface: string,
	rows: readonly DecisionRow[],
	gathered: ReadonlySet<string>,
): string {
	const byArea = new Map<string, DecisionRow[]>();
	for (const row of rows) {
		const area = dirname(row.path);
		const existing = byArea.get(area);
		if (existing === undefined) byArea.set(area, [row]);
		else existing.push(row);
	}
	const sections = [...byArea.keys()].sort().map((area) => {
		const table = renderDecisionIndex(byArea.get(area) ?? []);
		return `## ${basename(area)}\n\n${rewriteDesignLinks(table, `${area}/index.md`, gathered)}`;
	});
	return `${frontmatter(`${surface} decisions`)}\nEvery ${surface} decision, by area. Generated from the decision files at build time.\n\n${sections.join("\n")}`;
}

async function markdownFiles(directory: string): Promise<string[]> {
	const entries = await readdir(directory, { withFileTypes: true });
	const files: string[] = [];
	for (const entry of entries) {
		const path = join(directory, entry.name);
		if (entry.isDirectory()) files.push(...(await markdownFiles(path)));
		else if (entry.isFile() && entry.name.endsWith(".md")) files.push(path);
	}
	return files;
}

function frontmatterTitle(fields: unknown): string | undefined {
	if (typeof fields !== "object" || fields === null) return undefined;
	if ("title" in fields && typeof fields.title === "string")
		return fields.title;
	if ("page_title" in fields && typeof fields.page_title === "string")
		return fields.page_title;
	return undefined;
}

function splitFrontmatter(markdown: string): {
	body: string;
	fields: unknown;
} {
	const match = SOURCE_FRONTMATTER_RE.exec(markdown);
	if (match === null) return { body: markdown, fields: undefined };
	let fields: unknown;
	try {
		fields = YAML.parse(match[1] ?? "");
	} catch {
		// Source repos own their frontmatter; invalid YAML only loses the title.
		fields = undefined;
	}
	return { body: markdown.slice(match[0].length), fields };
}

function pageTitle(body: string, fields: unknown, sourcePath: string): string {
	const hasH1 = stripFirstH1(body) !== body;
	if (!hasH1) {
		const fallback = frontmatterTitle(fields);
		if (fallback !== undefined) return fallback;
	}
	return extractTitle(body, sourcePath);
}

async function writeSourcePages(
	contentRoot: string,
	sources: readonly FetchedSource[],
	pages: readonly BundlePage[],
	routes: ReadonlyMap<string, string>,
): Promise<void> {
	for (const source of sources) {
		for (const page of pages.filter(
			(candidate) => candidate.repo === source.repo,
		)) {
			const markdown = await readFile(
				join(source.root, page.sourcePath),
				"utf8",
			);
			const { body: markdownBody, fields } = splitFrontmatter(markdown);
			const title = pageTitle(markdownBody, fields, page.sourcePath);
			const body = rewriteBundleLinks(
				stripFirstH1(markdownBody),
				page,
				routes,
				source.root,
				(link) =>
					console.warn(
						`gather: broken link ${link.repo}/${link.sourcePath} -> ${link.target}`,
					),
			);
			const content = `---\ntitle: "${escapeTitle(title)}"\neditUrl: "https://github.com/${source.repo}/edit/main/${page.sourcePath}"\n---\n${body}`;
			const destination = join(
				contentRoot,
				page.route.replace(/^\/+/, ""),
				"index.md",
			);
			await mkdir(dirname(destination), { recursive: true });
			await writeFile(destination, content);
		}
	}
}

function escapeTitle(title: string): string {
	return title.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

async function loadPages(
	sources: readonly FetchedSource[],
): Promise<BundlePage[]> {
	const pages: BundlePage[] = [];
	for (const source of sources) {
		if (source.mount.replace(/^\/+|\/+$/g, "") === "designs")
			throw new Error(`source mount may not be designs: ${source.repo}`);
		const config = await readPublishConfig(source.root, source.config);
		pages.push(
			...remapSourceRoutes(await collectPages(source, config), source),
		);
	}
	return pages;
}

async function assertFetchedSources(
	siteRoot: string,
	sources: readonly FetchedSource[],
): Promise<void> {
	const configured = JSON.parse(
		await readFile(join(siteRoot, "sources.json"), "utf8"),
	) as unknown;
	if (!Array.isArray(configured))
		throw new Error("site/sources.json must contain an array");
	const fetched = new Set(sources.map((source) => source.repo));
	const missing = configured.flatMap((entry) =>
		typeof entry === "object" &&
		entry !== null &&
		"repo" in entry &&
		typeof entry.repo === "string" &&
		!fetched.has(entry.repo)
			? [entry.repo]
			: [],
	);
	if (missing.length > 0)
		throw new Error(`missing fetched source(s): ${missing.join(", ")}`);
}

async function bundlePages(
	contentRoot: string,
	sources: readonly FetchedSource[],
	designPages: readonly BundlePage[],
): Promise<BundlePage[]> {
	const fetchedPages = await loadPages(sources);
	const allPages = [...designPages, ...fetchedPages];
	assertNoRouteCollisions(allPages);
	const routes = new Map<string, string>(
		allPages.map((page): [string, string] => [
			`${page.repo}:${page.sourcePath}`,
			page.route,
		]),
	);
	await writeSourcePages(contentRoot, sources, fetchedPages, routes);
	return fetchedPages;
}

export interface GatherOptions {
	repoRoot: string;
	siteRoot: string;
	sources: FetchedSource[];
}

export async function gather(opts: GatherOptions): Promise<BundleManifest> {
	await assertFetchedSources(opts.siteRoot, opts.sources);
	const docsRoot = join(opts.repoRoot, "docs");
	const files = (await markdownFiles(docsRoot)).sort();
	const sources = new Map<string, string>();
	for (const file of files) {
		const sourcePath = relative(opts.repoRoot, file).split("\\").join("/");
		sources.set(sourcePath, await readFile(file, "utf8"));
	}
	const gathered = new Set(sources.keys());
	const contentRoot = join(opts.siteRoot, "src", "content", "docs");
	await rm(contentRoot, { recursive: true, force: true });
	const sourceList = opts.sources
		.map((source) => {
			const mount = source.mount.replace(/^\/+|\/+$/g, "");
			return `- [${source.repo.slice(source.repo.indexOf("/") + 1)}](/${mount}/)`;
		})
		.join("\n");
	const landing = join(contentRoot, "index.md");
	await mkdir(dirname(landing), { recursive: true });
	await writeFile(
		landing,
		`${frontmatter("Rigel public design docs")}\nDesign records and decisions for Rigel's public repositories.\n\n## Designs\n\nProjects are listed in the sidebar. Their design records and decisions are grouped by repository.\n\n## Code repositories\n\nEach bundled repository has its own sidebar group.\n\n${sourceList}\n`,
	);
	const decisions = new Map<string, DecisionRow[]>();
	const designPages: BundlePage[] = [];
	for (const [sourcePath, source] of sources) {
		const destination = join(contentRoot, sourcePath.replace(/^docs\//, ""));
		await mkdir(dirname(destination), { recursive: true });
		await writeFile(destination, transform(source, sourcePath, gathered));
		const route = routeSlug(sourcePath.replace(/^docs\//, ""));
		designPages.push({
			repo: REPO_SLUG,
			sourcePath,
			route,
			section: null,
			pagefind: true,
		});
		const surface = sourcePath.match(DECISION_PATH_RE)?.[1];
		if (surface !== undefined) {
			const parsed = parseDecisionFile(sourcePath, source);
			if (parsed.ok) {
				const existing = decisions.get(surface);
				if (existing === undefined) decisions.set(surface, [parsed.row]);
				else existing.push(parsed.row);
			}
		}
	}
	for (const [surface, rows] of decisions) {
		const destination = join(
			contentRoot,
			"designs",
			surface,
			"decisions",
			"index.md",
		);
		await mkdir(dirname(destination), { recursive: true });
		await writeFile(destination, buildDecisionIndex(surface, rows, gathered));
	}
	const fetchedPages = await bundlePages(
		contentRoot,
		opts.sources,
		designPages,
	);
	const manifest: BundleManifest = {
		docsRepo: { repo: REPO_SLUG, sha: process.env.GITHUB_SHA ?? "local" },
		sources: opts.sources.map((source) => ({
			repo: source.repo,
			ref: "main",
			sha: source.sha,
			pages: fetchedPages.filter((page) => page.repo === source.repo).length,
		})),
	};
	// Astro empties dist/ during the build, so the manifest is written to public/ and copied to dist/.
	await writeManifest(join(opts.siteRoot, "public"), manifest);
	return manifest;
}

if (import.meta.main) {
	const siteRoot = join(import.meta.dir, "..");
	const repoRoot = join(siteRoot, "..");
	const sources = await readFetched(join(siteRoot, ".bundle", "fetched.json"));
	const manifest = await gather({ repoRoot, siteRoot, sources });
	console.log(`gather: wrote ${manifest.sources.length} bundled sources`);
}
