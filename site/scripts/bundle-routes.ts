import type { BundlePage, FetchedSource } from "@rigelbuild/docs-bundle";

export function remapSourceRoutes(
	pages: readonly BundlePage[],
	source: FetchedSource,
): BundlePage[] {
	const repoPrefix = `/${source.repo}`;
	const mount = source.mount.replace(/^\/+|\/+$/g, "");
	const mountPrefix = mount.length === 0 ? "" : `/${mount}`;
	return pages.map((page) => {
		if (page.repo !== source.repo || !page.route.startsWith(repoPrefix))
			throw new Error(`unexpected route from docs bundle: ${page.route}`);
		const mounted = page.route.slice(repoPrefix.length);
		if (mountPrefix.length > 0 && !mounted.startsWith(mountPrefix))
			throw new Error(`route does not match source mount: ${page.route}`);
		const relativeRoute =
			mountPrefix.length === 0 ? mounted : mounted.slice(mountPrefix.length);
		const route =
			page.sourcePath.toLowerCase() === "readme.md"
				? mountPrefix || "/"
				: `${mountPrefix}${relativeRoute}`.replace(/\/index$/, "") || "/";
		return { ...page, route };
	});
}
