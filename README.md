# Rigel public design docs

Design records and decisions for Rigel's public repositories: meissa, the
forks, and every other public repo.

Each project gets its own directory, `docs/designs/<project>/`. Its decisions
live at `docs/designs/<project>/decisions/<area>/DL-NNN.md`.

Each fork has a directory named after its repository, with no owner prefix:
for example `docs/designs/oh-my-pi/` for `RigelBuild/oh-my-pi`. Fork design
records go there, not in the fork repository. A new fork gets its directory
when its first record lands.

Everything in this repository is public. Do not cite private repositories.

## Layout

Every public project has a directory at `docs/designs/<project>/`. Store design records there and decisions at `decisions/<area>/DL-NNN.md`. The ledger gate discovers project directories, so no config edit is needed for a new one.

## Claiming decision IDs

The counter uses the single public-repo surface `designs` for every project. The claim client needs an explicit surface list, so `bun run claim` uses `design-ledger.claim.config.json`; the gate config leaves `surfaces` out so it discovers projects.

```sh
bun run claim --ref RIG-<n> --lane <bookmark> --surface designs
```

Check the returned ID before creating `docs/designs/<project>/decisions/<area>/DL-NNN.md`. Do not rerun a failed claim blindly; the counter may have consumed an ID.

## Checks and site

Run `bun install`, `bun test`, `bun run gate`, `bun run ref-gate`, `rumdl check .`, `biome ci .`, and `bun run build`. The gather step reads checked-in Markdown under `docs/`, renders decision pages and per-project decision indexes into generated content, and the Astro Starlight site publishes to the Cloudflare Pages project `docs`.

The canonical source-repository dispatch workflow is `site/dispatch/docs-dispatch.yml`.

Cloudflare assigns the Pages hostname for project `docs`; the site does not configure a custom domain.

## Bundled sources

`site/sources.json` lists the code repos whose markdown the site bundles. `bun run fetch` clones each repo's `main` over anonymous HTTPS into `site/.bundle/`, and `bun run gather` turns their pages into site routes under `/<repo>/`. A source uses the `default` config (every gatherable markdown file) until it adds a `docs-bundle.toml`.

The deploy workflow runs the fetch before dependency install. After the build, `bun run ref-gate:dist` scans `site/dist` (minus `pagefind/`) and fails the deploy on a reference hit. The build writes `_bundle.json` with each source's SHA. Set `DOCS_BUNDLE_PINS="<repo>=<sha>"` to build a pinned SHA.
