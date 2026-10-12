# @rigelbuild/dl-claim

Claims decision IDs from the configured counter partition. The request repository, surface list, and counter URL come from the design-ledger configuration.

## Usage

```sh
bun packages/dl-claim/index.ts --config ./ledger.config.json --ref RIG-1234 --lane feature/decision
bun packages/dl-claim/index.ts --config ./ledger.config.json --ref none --lane feature/decision --count 3 --surface alpha
```

`--ref` accepts `RIG-n` or `none`. `--lane` identifies the claims. `--count` is 1 to 10 and defaults to 1. `--surface` is required when the config lists multiple surfaces; a single configured surface is selected by default.

A failed request may have consumed IDs. Do not rerun it blindly. The error includes the configured counter URL and partition for checking the lane's claims.

## Credentials

`DL_CLAIM_TOKEN` supplies the bearer token. A non-empty trimmed `DL_CLAIM_TOKEN_FILE` value takes precedence. The counter URL is read from `counter.url` in the config; there is no environment override.

## Exit codes

- `0`: IDs claimed.
- `1`: counter request or service failure.
- `2`: usage, configuration, or read error.
