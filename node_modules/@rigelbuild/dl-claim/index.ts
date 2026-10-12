#!/usr/bin/env bun
import type { LedgerConfig } from "@rigelbuild/design-ledger-gate";
import {
	loadLedgerConfig,
	readDlClaimToken,
	validateDlClaimToken,
} from "@rigelbuild/design-ledger-gate";

export interface ClaimArgs {
	ref: string;
	lane: string;
	count: number;
	surface?: string;
}

export interface ClaimRequest {
	repo: string;
	surface: string;
	ref: string;
	lane: string;
	count: number;
}

export interface ClaimedId {
	id: string;
	date: string;
}

const USAGE =
	"Usage: dl-claim --config <path> --ref <RIG-n|none> --lane <branch> [--count 1..10] [--surface <s>]";
const ACCEPTED_FLAGS = ["--ref", "--lane", "--count", "--surface"];

function parseFlag(arg: string): { flag: string; value: string | undefined } {
	const equalIndex = arg.indexOf("=");
	return equalIndex < 0
		? { flag: arg, value: undefined }
		: { flag: arg.slice(0, equalIndex), value: arg.slice(equalIndex + 1) };
}

function readFlagValue(
	argv: readonly string[],
	index: number,
	flag: string,
	inlineValue: string | undefined,
): { value: string; nextIndex: number } {
	const value = inlineValue ?? argv[index + 1];
	if (value === undefined || value.startsWith("--")) {
		throw new Error(`${USAGE}\nMissing value for ${flag}`);
	}
	return { value, nextIndex: inlineValue === undefined ? index + 1 : index };
}

export function parseArgs(
	argv: readonly string[],
	config: LedgerConfig,
): ClaimArgs {
	const state: {
		ref: string | undefined;
		lane: string | undefined;
		count: number;
		surface: string | undefined;
	} = { ref: undefined, lane: undefined, count: 1, surface: undefined };
	for (let index = 0; index < argv.length; index += 1) {
		const arg = argv[index] ?? "";
		const { flag, value: inlineValue } = parseFlag(arg);
		if (!ACCEPTED_FLAGS.includes(flag))
			throw new Error(`${USAGE}\nUnknown flag: ${arg}`);
		const parsed = readFlagValue(argv, index, flag, inlineValue);
		index = parsed.nextIndex;
		const value = parsed.value;
		switch (flag) {
			case "--ref":
				state.ref = value;
				break;
			case "--lane":
				state.lane = value;
				break;
			case "--count":
				if (!/^\d+$/.test(value))
					throw new Error(`${USAGE}\nInvalid count: ${value}`);
				state.count = Number(value);
				break;
			case "--surface":
				state.surface = value;
				break;
		}
	}
	const ref = state.ref;
	if (ref === undefined) throw new Error(`${USAGE}\n--ref is required`);
	if (!/^(RIG-\d+|none)$/.test(ref))
		throw new Error(`${USAGE}\nInvalid ref: ${ref}`);
	const lane = state.lane;
	if (lane === undefined || lane.trim().length === 0)
		throw new Error(`${USAGE}\n--lane must be non-empty`);
	if (!Number.isInteger(state.count) || state.count < 1 || state.count > 10)
		throw new Error(`${USAGE}\nCount must be an integer from 1 to 10`);
	const surfaces = config.surfaces ?? [];
	if (surfaces.length === 0)
		throw new Error("config.surfaces must contain at least one surface");
	const surface =
		state.surface ?? (surfaces.length === 1 ? surfaces[0] : undefined);
	if (surface === undefined) {
		throw new Error(`${USAGE}\n--surface is required for multiple surfaces`);
	}
	if (!surfaces.includes(surface))
		throw new Error(`${USAGE}\nUnknown surface: ${surface}`);
	return { ref, lane, count: state.count, surface };
}

export function buildClaimBody(
	config: LedgerConfig,
	args: ClaimArgs,
): ClaimRequest {
	const surfaces = config.surfaces ?? [];
	const surface =
		args.surface ?? (surfaces.length === 1 ? surfaces[0] : undefined);
	if (surface === undefined || !surfaces.includes(surface))
		throw new Error("a valid surface from config.surfaces is required");
	return {
		repo: config.counter.partition,
		surface,
		ref: args.ref,
		lane: args.lane,
		count: args.count,
	};
}

/** The seam injects only the request call used by the CLI. */
type FetchFn = (
	input: string | URL | Request,
	init?: RequestInit,
) => Promise<Response>;

export interface ClaimDeps {
	fetchFn?: FetchFn;
	timeoutMs?: number;
	timeoutSignal?: (timeoutMs: number) => AbortSignal;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function baseUrl(config: LedgerConfig): string {
	return config.counter.url.replace(/\/+$/, "");
}

function maybeMinted(config: LedgerConfig): string {
	const statusUrl = `${baseUrl(config)}/status?repo=${encodeURIComponent(config.counter.partition)}`;
	return ` Ids may already be consumed; do not rerun. Find this lane's claims with \`curl -H "Authorization: Bearer $DL_CLAIM_TOKEN" '${statusUrl}'\`.`;
}

async function readServiceError(
	response: Response,
): Promise<string | undefined> {
	try {
		const payload: unknown = await response.json();
		return isRecord(payload) && typeof payload.error === "string"
			? payload.error
			: undefined;
	} catch {
		return undefined;
	}
}

function formatServiceError(
	config: LedgerConfig,
	status: number,
	code: string | undefined,
): Error {
	const hint =
		status === 401
			? " Token missing or wrong."
			: status === 429
				? " Rate limited; retry later."
				: status === 503 && code === "not_hydrated"
					? ` The reconcile process has not hydrated the ${config.counter.partition} partition yet.`
					: status === 400
						? ""
						: maybeMinted(config);
	return new Error(
		`claim failed with HTTP ${status}${code ? ` (${code})` : ""}.${hint}`,
	);
}

function isClaimedId(value: unknown): value is ClaimedId {
	return (
		isRecord(value) &&
		typeof value.id === "string" &&
		/^DL-\d{3,}$/.test(value.id) &&
		typeof value.date === "string" &&
		/^\d{4}-\d{2}-\d{2}$/.test(value.date)
	);
}

async function readClaimedIds(
	response: Response,
	count: number,
	config: LedgerConfig,
): Promise<ClaimedId[]> {
	let payload: unknown;
	try {
		payload = await response.json();
	} catch {
		throw new Error(`claim response is not valid JSON.${maybeMinted(config)}`);
	}
	const rawIds: readonly unknown[] | undefined =
		isRecord(payload) && Array.isArray(payload.ids) ? payload.ids : undefined;
	const returned = (rawIds ?? []).flatMap((entry) =>
		isRecord(entry) && typeof entry.id === "string" && /^DL-\d+$/.test(entry.id)
			? [entry.id]
			: [],
	);
	const seen = returned.length > 0 ? ` Returned: ${returned.join(", ")}.` : "";
	if (rawIds === undefined || rawIds.length !== count)
		throw new Error(
			`claim response must contain the requested number of ids.${seen}${maybeMinted(config)}`,
		);
	const claimed: ClaimedId[] = [];
	for (const entry of rawIds) {
		if (!isClaimedId(entry))
			throw new Error(
				`claim response contains a malformed id.${seen}${maybeMinted(config)}`,
			);
		claimed.push(entry);
	}
	return claimed;
}

export async function claim(
	body: ClaimRequest,
	token: string,
	config: LedgerConfig,
	deps: ClaimDeps = {},
): Promise<ClaimedId[]> {
	const trimmedToken = validateDlClaimToken(token);
	const timeoutMs =
		deps.timeoutMs !== undefined && deps.timeoutMs > 0
			? deps.timeoutMs
			: 30_000;
	let response: Response;
	try {
		response = await (deps.fetchFn ?? fetch)(`${baseUrl(config)}/claim`, {
			method: "POST",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${trimmedToken}`,
				"Content-Type": "application/json",
			},
			body: JSON.stringify(body),
			signal: (deps.timeoutSignal ?? AbortSignal.timeout)(timeoutMs),
		});
	} catch (error) {
		const name = error instanceof Error ? error.name : "UnknownError";
		throw new Error(
			`request to ${baseUrl(config)}/claim failed (${name}).${maybeMinted(config)}`,
		);
	}
	if (!response.ok)
		throw formatServiceError(
			config,
			response.status,
			await readServiceError(response),
		);
	return readClaimedIds(response, body.count, config);
}

function extractConfigPath(argv: readonly string[]): {
	configPath: string;
	claimArgs: string[];
} {
	let configPath: string | undefined;
	const claimArgs: string[] = [];
	for (let index = 0; index < argv.length; index += 1) {
		const arg = argv[index] ?? "";
		if (arg === "--config" || arg.startsWith("--config=")) {
			if (configPath !== undefined)
				throw new Error(`${USAGE}\n--config may appear once`);
			const inlineValue =
				arg === "--config" ? undefined : arg.slice("--config=".length);
			const parsed = readFlagValue(argv, index, "--config", inlineValue);
			if (parsed.value.length === 0)
				throw new Error(`${USAGE}\n--config requires a path`);
			configPath = parsed.value;
			index = parsed.nextIndex;
		} else {
			claimArgs.push(arg);
		}
	}
	if (configPath === undefined)
		throw new Error(`${USAGE}\n--config is required`);
	return { configPath, claimArgs };
}

export interface ClaimRunDeps {
	token?: string;
	fetchFn?: FetchFn;
	log?: (message: string) => void;
	err?: (message: string) => void;
}

export async function runOnce(
	argv: readonly string[],
	deps: ClaimRunDeps = {},
): Promise<number> {
	const log = deps.log ?? console.log;
	const err = deps.err ?? console.error;
	if (argv.length === 1 && argv[0] === "--help") {
		log(USAGE);
		return 0;
	}
	let configPath: string;
	let claimArgs: string[];
	try {
		({ configPath, claimArgs } = extractConfigPath(argv));
	} catch (error) {
		err(`dl-claim: ${error instanceof Error ? error.message : String(error)}`);
		return 2;
	}
	let config: LedgerConfig;
	try {
		config = loadLedgerConfig(configPath);
	} catch (error) {
		err(`dl-claim: ${error instanceof Error ? error.message : String(error)}`);
		return 2;
	}
	let body: ClaimRequest;
	try {
		body = buildClaimBody(config, parseArgs(claimArgs, config));
	} catch (error) {
		err(`dl-claim: ${error instanceof Error ? error.message : String(error)}`);
		return 2;
	}
	let token: string;
	try {
		token = validateDlClaimToken(deps.token ?? (await readDlClaimToken()));
	} catch (error) {
		err(
			`dl-claim: ${error instanceof Error ? error.message : "invalid token"}`,
		);
		return 2;
	}
	try {
		const ids = await claim(body, token, config, {
			...(deps.fetchFn === undefined ? {} : { fetchFn: deps.fetchFn }),
		});
		log(formatClaimed(ids));
		return 0;
	} catch (error) {
		// claim builds every message it throws; none carries the token.
		err(`dl-claim: ${error instanceof Error ? error.message : String(error)}`);
		return 1;
	}
}
export function formatClaimed(ids: readonly ClaimedId[]): string {
	return ids.map(({ id, date }) => `${id} (claimed ${date})`).join("\n");
}

if (import.meta.main) process.exitCode = await runOnce(process.argv.slice(2));
