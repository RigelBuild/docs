import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";

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
