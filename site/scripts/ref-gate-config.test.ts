import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import { findViolations, loadRefGateConfig } from "@rigelbuild/ref-gate";

const config = loadRefGateConfig(
	join(import.meta.dir, "../../ref-gate.config.json"),
);
const flagged = (text: string) =>
	findViolations(config, [`docs/x.md\u00001\u0000${text}`]).length > 0;

describe("ref-gate config", () => {
	test("allows only the exact krypton gate name", () => {
		for (const text of [
			"orion-ref-gate",
			"`moon run orion-ref-gate:check` green",
			"(`tools/orion-ref-gate/index.test.ts`,",
		])
			expect(flagged(text)).toBe(false);
	});

	test("flags the private repo name and gate-name continuations", () => {
		for (const text of [
			"orion repo",
			"orion-ref-gate-private",
			"ORION-REF-GATE-private",
			"orion-ref-gateX",
			"orion-ref-gate_x",
			"orion-ref-gate and orion",
		])
			expect(flagged(text)).toBe(true);
	});
});
