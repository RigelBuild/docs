# @rigelbuild/ref-gate

A configurable gate that finds references in tracked Git files. The POSIX ERE prefilter narrows Git's scan; JavaScript regular expressions decide which hits are violations.

## Configuration

Pass a JSON file with these fields:

```json
{
  "prefilter": { "ere": "acme", "ignoreCase": true },
  "patterns": [{ "source": "\\bacme\\b", "flags": "i" }],
  "ignore": ["acme-gate"],
  "carveOutPaths": ["generated/manifest.json"],
  "carveOutPrefixes": ["generated/docs/"],
  "allowlist": { "fixtures/example.txt": "Synthetic fixture" },
  "remediationDoc": "docs/reference-policy.md"
}
```

`prefilter.ere` is a POSIX extended regular expression passed directly to `git grep`; it is not derived from `patterns`. The other fields use JavaScript regular expressions or repo-relative paths. `remediationDoc` is optional.

Every tracked file is scanned as text, including files Git treats as binary, so a NUL byte or a `.gitattributes` mark cannot hide one. List real binaries in `carveOutPaths` or `carveOutPrefixes`.

## CLI

```sh
ref-gate --config ./ref-gate.json
```

`GATE_ROOT` optionally sets the Git scan root. Otherwise the current Git root is used.

## Exit codes

- `0`: no violations.
- `1`: one or more violations; each is printed as `<file>:<line>: <text>`.
- `2`: usage, configuration, or scan error.
