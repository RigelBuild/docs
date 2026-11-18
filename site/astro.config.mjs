import { readFileSync } from "node:fs";
import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";

const sources = JSON.parse(
	readFileSync(new URL("./sources.json", import.meta.url), "utf8"),
);

export default defineConfig({
	output: "static",
	integrations: [
		starlight({
			title: "Rigel public design docs",
			description:
				"Design records and decisions for Rigel's public repositories.",
			sidebar: [
				{
					label: "Designs",
					items: [{ autogenerate: { directory: "designs" } }],
				},
				...sources.map((source) => ({
					label: source.repo.slice(source.repo.indexOf("/") + 1),
					items: [
						{
							autogenerate: {
								directory: source.mount.replace(/^\/+|\/+$/g, ""),
							},
						},
					],
				})),
			],
			customCss: ["./src/styles/fonts.css", "./src/styles/custom.css"],
			// A bundled light Shiki theme keeps code samples readable on the light surface.
			expressiveCode: {
				themes: ["github-light"],
				useStarlightDarkModeSwitch: false,
				themeCssSelector: false,
			},
			tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 4 },
		}),
	],
});
