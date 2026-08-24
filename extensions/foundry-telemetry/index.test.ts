/**
 * Mocked-fetch verification for foundry-telemetry's delivery and evidence
 * fidelity guarantees:
 *
 *   1. Concurrent source-session isolation: producer IDs, sequence files,
 *      records, and acknowledgement compaction are session-owned and recover
 *      independently after restart.
 *   2. Reload lifecycle: shutdown durably appends session_end, aborts the old
 *      worker, and waits until it cannot acknowledge/compact the shared spool.
 *   3. Ordering: telemetry POSTs are delivered to `fetch` in event order
 *      (session_start before activity), while session_end is durably queued
 *      after prior events, even when individual network calls resolve out of
 *      order.
 *   4. Failure continuation: a failing/rejecting POST does not stop later
 *      events from being sent (fail-open) and does not break ordering.
 *   5. Reasoning/thinking/tool-call/image exclusion: `final_message`
 *      content only carries plain text blocks.
 *   6. No empty evidence: reasoning-only or tool-call-only assistant
 *      responses (no plain-text block) never produce a `final_message`
 *      event, while the usage-bearing `message_end` event is still sent.
 *   7. User messages: a completed, non-empty plain-text user message is
 *      captured as `final_message` evidence in serialized event order
 *      (e.g. before a later assistant `final_message`), while producing
 *      no `message_end`/usage row and no delta/reasoning/image/
 *      tool-result capture. Empty or non-text-only user content produces
 *      no `final_message` at all. Assistant usage/privacy behavior is
 *      unaffected.
 *
 * Runnable directly with Node's built-in TypeScript stripping:
 *   node --experimental-strip-types extensions/foundry-telemetry/index.test.ts
 *
 * No test framework or npm install required — matches the extension's own
 * zero-runtime-dependency constraint. Assertions throw on failure and the
 * process exits non-zero; a final "PASS" line is printed once all checks
 * complete.
 */

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { promises as fs } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BatchDelivery } from "./delivery.ts";
import { DiskSpool, sessionProducerId, sessionSpoolPath } from "./spool.ts";
import type { SpoolFileSystem } from "./spool.ts";

type Handler = (event: any, ctx: any) => Promise<void> | void;

const CAPTURE_TEST_ROOT = process.cwd();

let extensionCounter = 0;
function configureTelemetryTest(): string {
	process.env.FOUNDRY_TELEMETRY_URL = "http://mock.invalid/api/telemetry/events";
	process.env.FOUNDRY_TELEMETRY_PRODUCER_ID = "pi:test-host";
	process.env.FOUNDRY_TELEMETRY_TRUSTED_ROOTS = CAPTURE_TEST_ROOT;
	delete process.env.FOUNDRY_TELEMETRY_BEARER_TOKEN;
	process.env.FOUNDRY_TELEMETRY_SPOOL_PATH = join(
		tmpdir(),
		`foundry-telemetry-index-test-${process.pid}-${extensionCounter++}`,
		"events.jsonl",
	);
	return process.env.FOUNDRY_TELEMETRY_SPOOL_PATH;
}

/** Minimal fake ExtensionAPI capturing registered event handlers. */
function makeFakePi() {
	const handlers = new Map<string, Handler[]>();
	const commands = new Map<string, any>();
	return {
		api: {
			on(name: string, handler: Handler) {
				const list = handlers.get(name) ?? [];
				list.push(handler);
				handlers.set(name, list);
			},
			registerCommand(name: string, spec: unknown) {
				commands.set(name, spec);
			},
		},
		async fire(name: string, event: any, ctx: any) {
			for (const handler of handlers.get(name) ?? []) {
				await handler(event, ctx);
			}
		},
		async command(name: string, ctx: any) {
			await commands.get(name).handler("", ctx);
		},
	};
}

function deferred<T>() {
	let resolve!: (value: T | PromiseLike<T>) => void;
	const promise = new Promise<T>((done) => {
		resolve = done;
	});
	return { promise, resolve };
}

/** A fetch which remains in-flight until explicitly released by the test. */
function installBlockedFetch() {
	const called = deferred<void>();
	const release = deferred<void>();
	const originalFetch = globalThis.fetch;
	globalThis.fetch = (async (_url: string, init: any) => {
		called.resolve();
		await release.promise;
		const events = JSON.parse(init.body as string).events as any[];
		return {
			ok: true,
			status: 200,
			json: async () => ({
				results: events.map((event, index) => ({ index, event_id: event.event_id, status: "accepted" })),
			}),
		} as Response;
	}) as typeof fetch;
	return {
		called: called.promise,
		release: () => release.resolve(),
		restore: () => {
			globalThis.fetch = originalFetch;
		},
	};
}

function makeCtx(overrides: Record<string, unknown> = {}) {
	return {
		cwd: CAPTURE_TEST_ROOT,
		mode: "default",
		model: { provider: "acme", id: "model-1" },
		sessionManager: {
			getSessionId: () => "session-abc",
			getHeader: () => ({}),
		},
		ui: { notify: () => {} },
		...overrides,
	};
}

/** Records POST bodies in the order `fetch` was actually invoked. */
function installMockFetch(opts: {
	// Per-call artificial delay (ms) before resolving/rejecting, keyed by
	// the telemetry event `type` in the request body. Lets us simulate
	// out-of-order network completion to prove queueing (not just call
	// order) is what determines delivery order.
	delayByType?: Record<string, number>;
	// Event types that should reject/fail instead of succeeding.
	failTypes?: Set<string>;
}) {
	const delivered: any[] = [];
	const originalFetch = globalThis.fetch;
	const failedOnce = new Set<string>();
	globalThis.fetch = (async (_url: string, init: any) => {
		const envelope = JSON.parse(init.body as string);
		const events = envelope.events as any[];
		const firstType = events[0]?.type;
		const delay = opts.delayByType?.[firstType] ?? 0;
		if (delay > 0) await new Promise((resolve) => setTimeout(resolve, delay));
		const failing = events.find((event) => opts.failTypes?.has(event.type) && !failedOnce.has(event.type));
		if (failing) {
			failedOnce.add(failing.type);
			throw new Error(`simulated failure for ${failing.type}`);
		}
		delivered.push(...events);
		return {
			ok: true,
			status: 200,
			json: async () => ({
				results: events.map((event, index) => ({ index, event_id: event.event_id, status: "accepted" })),
			}),
		} as Response;
	}) as typeof fetch;
	return {
		delivered,
		restore() {
			globalThis.fetch = originalFetch;
		},
	};
}

async function testSessionStartTrustAndAuthenticationConfiguration() {
	const root = await fs.mkdtemp(join(tmpdir(), "foundry-telemetry-trust-"));
	const trustedRoot = join(root, "trusted");
	const trustedProject = join(trustedRoot, "project");
	const prefixCollision = join(root, "trusted-other");
	const outside = join(root, "outside");
	await Promise.all([
		fs.mkdir(trustedProject, { recursive: true }),
		fs.mkdir(prefixCollision, { recursive: true }),
		fs.mkdir(outside, { recursive: true }),
	]);
	await fs.symlink(outside, join(trustedRoot, "escape"));

	const originalFetch = globalThis.fetch;
	const requests: Array<{ headers: Record<string, string>; events: any[] }> = [];
	globalThis.fetch = (async (_url: string, init: any) => {
		const events = JSON.parse(init.body as string).events as any[];
		requests.push({ headers: init.headers, events });
		return {
			ok: true,
			status: 200,
			json: async () => ({
				results: events.map((event, index) => ({ index, event_id: event.event_id, status: "accepted" })),
			}),
		} as Response;
	}) as typeof fetch;

	try {
		const mod = await import("./index.ts");

		configureTelemetryTest();
		delete process.env.FOUNDRY_TELEMETRY_TRUSTED_ROOTS;
		const disabled = makeFakePi();
		mod.default(disabled.api as any);
		await disabled.fire("session_start", {}, makeCtx({ cwd: trustedProject }));
		let status = "";
		await disabled.command("foundry-telemetry", makeCtx({ ui: { notify: (text: string) => { status = text; } } }));
		assert.match(status, /capture: disabled:/);
		assert.equal(requests.length, 0, "telemetry must default off without configured trusted roots");

		configureTelemetryTest();
		process.env.FOUNDRY_TELEMETRY_TRUSTED_ROOTS = trustedRoot;
		const trusted = makeFakePi();
		mod.default(trusted.api as any);
		await trusted.fire("session_start", {}, makeCtx({ cwd: trustedProject }));
		await new Promise((resolve) => setTimeout(resolve, 30));
		assert.equal(requests.length, 1, "cwd beneath a trusted root was not captured");

		for (const cwd of [prefixCollision, join(trustedRoot, "escape")]) {
			configureTelemetryTest();
			process.env.FOUNDRY_TELEMETRY_TRUSTED_ROOTS = trustedRoot;
			const untrusted = makeFakePi();
			mod.default(untrusted.api as any);
			await untrusted.fire("session_start", {}, makeCtx({ cwd }));
			let untrustedStatus = "";
			await untrusted.command("foundry-telemetry", makeCtx({ ui: { notify: (text: string) => { untrustedStatus = text; } } }));
			assert.match(untrustedStatus, /capture: untrusted:/);
		}
		assert.equal(requests.length, 1, "prefix collision or symlink escape was incorrectly trusted");

		configureTelemetryTest();
		process.env.FOUNDRY_TELEMETRY_TRUSTED_ROOTS = trustedRoot;
		process.env.FOUNDRY_TELEMETRY_BEARER_TOKEN = "secret-test-token";
		const authenticated = makeFakePi();
		mod.default(authenticated.api as any);
		await authenticated.fire("session_start", {}, makeCtx({ cwd: trustedProject }));
		await new Promise((resolve) => setTimeout(resolve, 30));
		assert.equal(requests.at(-1)?.headers.authorization, "Bearer secret-test-token");
		let authenticatedStatus = "";
		await authenticated.command("foundry-telemetry", makeCtx({ ui: { notify: (text: string) => { authenticatedStatus = text; } } }));
		assert.ok(!authenticatedStatus.includes("secret-test-token"), "status exposed bearer credentials");
	} finally {
		globalThis.fetch = originalFetch;
		delete process.env.FOUNDRY_TELEMETRY_BEARER_TOKEN;
		await fs.rm(root, { recursive: true, force: true });
	}
	console.log("PASS: session capture requires realpath-trusted roots and sends hidden bearer credentials");
}

async function testPartialBatchAcknowledgementAndDuplicateReplay() {
	const root = await fs.mkdtemp(join(tmpdir(), "foundry-telemetry-partial-batch-"));
	try {
		const spool = new DiskSpool({ path: join(root, "events.jsonl"), producerId: "producer" });
		await spool.append({ type: "session_start" });
		await spool.append({ type: "tool_use" });
		await spool.append({ type: "final_message" });

		let sent = 0;
		let dropped = 0;
		const delivery = new BatchDelivery({
			spool,
			endpoint: () => "http://mock.invalid/api/telemetry/events",
			fetch: (async (_url: string, init: any) => {
				const events = JSON.parse(init.body as string).events as any[];
				return {
					ok: true,
					status: 200,
					json: async () => ({
						results: [
							{ index: 0, event_id: events[0].event_id, status: "duplicate" },
							{ index: 1, event_id: events[1].event_id, status: "rejected", error: "invalid" },
							{ index: 2, event_id: events[2].event_id, status: "accepted" },
						],
					}),
				} as Response;
			}) as typeof fetch,
			onSent: (count) => { sent += count; },
			onDropped: (count) => { dropped += count; },
		});
		await delivery.drain(1_000);

		assert.equal((await spool.peek(10)).length, 0, "known mixed outcomes were replayed instead of compacted");
		assert.equal(sent, 2, "accepted and duplicate outcomes should both complete delivery");
		assert.equal(dropped, 1, "only the rejected event should be counted as dropped");
		await delivery.stop();
	} finally {
		await fs.rm(root, { recursive: true, force: true });
	}
	console.log("PASS: partial rejection and duplicate replay compact one acknowledged batch");
}

async function testCrashRestartSpoolRecovery() {
	const root = await fs.mkdtemp(join(tmpdir(), "foundry-telemetry-crash-recovery-"));
	try {
		const path = join(root, "events.jsonl");
		const spool = new DiskSpool({ path, producerId: "producer" });
		await spool.append({ type: "session_start" });
		await spool.append({ type: "tool_use" });
		await fs.appendFile(path, '{"producer_id":"producer","event_id":"partial', "utf8");

		const recovered = new DiskSpool({ path, producerId: "producer" });
		await recovered.ready();
		assert.deepEqual((await recovered.peek(10)).map((record) => record.event.type), ["session_start", "tool_use"]);
		const next = await recovered.append({ type: "session_end" });
		assert.equal(next?.client_seq, 3, "restart reused a sequence after a torn final append");
	} finally {
		await fs.rm(root, { recursive: true, force: true });
	}
	console.log("PASS: crash/restart recovers complete spool records and sequence state");
}

async function testSpoolQueueBounds() {
	const root = await fs.mkdtemp(join(tmpdir(), "foundry-telemetry-queue-bounds-"));
	try {
		const eventBound = new DiskSpool({ path: join(root, "events.jsonl"), producerId: "events", maxEvents: 2 });
		assert.ok(await eventBound.append({ type: "one" }));
		assert.ok(await eventBound.append({ type: "two" }));
		assert.equal(await eventBound.append({ type: "three" }), undefined);
		assert.deepEqual(await eventBound.stats(), {
			diskEvents: 2,
			diskBytes: (await eventBound.peek(10)).reduce((sum, record) => sum + record.bytes, 0),
			dropped: 1,
		});

		const probe = new DiskSpool({ path: join(root, "probe.jsonl"), producerId: "bytes" });
		const first = await probe.append({ type: "one" });
		assert.ok(first);
		const firstBytes = (await probe.peek(1))[0].bytes;
		const byteBound = new DiskSpool({
			path: join(root, "byte-events.jsonl"),
			producerId: "bytes",
			maxBytes: firstBytes,
		});
		assert.ok(await byteBound.append({ type: "one" }));
		assert.equal(await byteBound.append({ type: "two" }), undefined);
		assert.equal((await byteBound.stats()).diskEvents, 1);
	} finally {
		await fs.rm(root, { recursive: true, force: true });
	}
	console.log("PASS: disk spool enforces event and byte queue bounds");
}

async function testHandlerWaitsForAppendDurability() {
	const { api, fire } = makeFakePi();
	configureTelemetryTest();
	const appendStarted = deferred<void>();
	const allowAppend = deferred<void>();
	const appendDurable = deferred<void>();
	const gatedFs: SpoolFileSystem = {
		mkdir: (...args) => fs.mkdir(...args),
		readFile: (path, encoding) => fs.readFile(path, encoding),
		writeFile: (path, data, encoding) => fs.writeFile(path, data, encoding),
		appendFile: async (path, data, encoding) => {
			appendStarted.resolve();
			await allowAppend.promise;
			await fs.appendFile(path, data, encoding);
			appendDurable.resolve();
		},
		rename: (from, to) => fs.rename(from, to),
	};
	const network = installBlockedFetch();
	const mod = await import("./index.ts");
	mod.default(api as any, { spoolFileSystem: gatedFs });

	let completed = false;
	const handler = fire("session_start", {}, makeCtx()).then(() => {
		completed = true;
	});
	await appendStarted.promise;
	await Promise.resolve();
	assert.equal(completed, false, "session_start returned before its spool append completed");
	allowAppend.resolve();
	await appendDurable.promise;
	await handler;
	assert.equal(completed, true);

	network.release();
	network.restore();
	console.log("PASS: handler completion occurs after serialized append durability");
}

async function testNetworkOutageDoesNotDelayHandler() {
	const { api, fire } = makeFakePi();
	configureTelemetryTest();
	const network = installBlockedFetch();
	const mod = await import("./index.ts");
	mod.default(api as any);
	const ctx = makeCtx();

	await fire("session_start", {}, ctx);
	await network.called;
	const toolHandler = fire(
		"tool_execution_start",
		{ toolCallId: "blocked-network-call", toolName: "bash", args: { cmd: "true" } },
		ctx,
	);
	const outcome = await Promise.race([
		toolHandler.then(() => "handler"),
		new Promise<string>((resolve) => setTimeout(() => resolve("timeout"), 100)),
	]);
	assert.equal(outcome, "handler", "handler latency was extended by an in-flight network outage");

	network.release();
	network.restore();
	console.log("PASS: network outage does not extend handler latency");
}

async function testImmediateRestartRecoversHandlerEvent() {
	const { api, fire } = makeFakePi();
	const basePath = configureTelemetryTest();
	const network = installBlockedFetch();
	const mod = await import("./index.ts");
	mod.default(api as any);

	await fire("session_start", {}, makeCtx());
	const sourceSessionId = "session-abc";
	const recovered = new DiskSpool({
		path: sessionSpoolPath(basePath, sourceSessionId),
		producerId: sessionProducerId("pi:test-host", sourceSessionId),
	});
	await recovered.ready();
	const records = await recovered.peek(10);
	assert.equal(records.length, 1, "immediate restart did not recover the appended event");
	assert.equal(records[0].event.type, "session_start");

	network.release();
	network.restore();
	console.log("PASS: immediate restart recovers the event appended before handler return");
}

async function testOrderingAndFailureContinuation() {
	const { api, fire } = makeFakePi();
	// session_start's request is the slowest to resolve; tool_use/tool_result
	// resolve fast; tool_use is also made to fail. If ordering were only a
	// function of promise resolution speed (rather than an explicit queue),
	// the fast, failing tool_use call could be observed by `fetch` before
	// session_start, or session_end could race ahead of the tool events.
	const mock = installMockFetch({
		delayByType: { session_start: 30 },
		failTypes: new Set(["tool_use"]),
	});

	configureTelemetryTest();
	const mod = await import("./index.ts");
	mod.default(api as any);

	const ctx = makeCtx();
	await fire("session_start", {}, ctx);
	await fire(
		"tool_execution_start",
		{ toolCallId: "call-1", toolName: "bash", args: { cmd: "echo hi" } },
		ctx,
	);
	await fire(
		"tool_execution_end",
		{ toolCallId: "call-1", toolName: "bash", result: { content: "hi" }, isError: false },
		ctx,
	);
	// Wait past every artificial delay and retry backoff so activity queued
	// before shutdown has settled. session_end itself is durability-only: stop
	// may leave it for the replacement runtime rather than extending shutdown
	// with network delivery.
	await new Promise((resolve) => setTimeout(resolve, 600));
	await fire("session_shutdown", {}, ctx);

	const types = mock.delivered.map((d) => d.type);
	assert.deepEqual(
		types,
		["session_start", "tool_use", "tool_result"],
		`expected strict event order, got: ${JSON.stringify(types)}`,
	);

	mock.restore();
	console.log("PASS: ordering preserved despite slow session_start and failing tool_use");
}

async function testPreSpoolSecretRedactionAndToolBodyDenylist() {
	const { api, fire } = makeFakePi();
	configureTelemetryTest();
	const diskWrites: string[] = [];
	const recordingFs: SpoolFileSystem = {
		mkdir: (...args) => fs.mkdir(...args),
		readFile: (path, encoding) => fs.readFile(path, encoding),
		writeFile: async (path, data, encoding) => {
			diskWrites.push(data);
			await fs.writeFile(path, data, encoding);
		},
		appendFile: async (path, data, encoding) => {
			diskWrites.push(data);
			await fs.appendFile(path, data, encoding);
		},
		rename: (from, to) => fs.rename(from, to),
	};
	const mock = installMockFetch({});
	const mod = await import("./index.ts");
	mod.default(api as any, { spoolFileSystem: recordingFs, toolBodyDenylist: ["secret_tool"] });

	// AWS access key IDs are AKIA followed by exactly 16 uppercase letters or
	// digits. Keep realistic full-length fixtures here so a short match cannot
	// redact only a prefix and leak the final four characters.
	const awsAccessKeyIDs = [
		"AKIAIOSFODNN7EXAMPLE",
		"AKIA1234567890ABWXYZ",
	];
	const secrets = [
		"api-original-123456",
		"ghp_OriginalToken123456",
		"password-original-123",
		"authorization-original-123",
		"PRIVATE-MATERIAL-ORIGINAL",
		"denied-input-original",
		"denied-result-original",
		...awsAccessKeyIDs,
	];
	const privateKey = `-----BEGIN PRIVATE KEY-----\n${secrets[4]}\n-----END PRIVATE KEY-----`;
	const ctx = makeCtx();
	await fire("session_start", {}, ctx);
	await fire("tool_execution_start", {
		toolCallId: "allowed-call",
		toolName: "bash",
		args: {
			api_key: secrets[0],
			token: secrets[1],
			password: secrets[2],
			authorization: `Bearer ${secrets[3]}`,
			private_key: privateKey,
			// Punctuation exercises both token boundaries without relying on a
			// sensitive assignment key to trigger redaction.
			command: `aws calls (${awsAccessKeyIDs[0]}) and [${awsAccessKeyIDs[1]}]`,
		},
	}, ctx);
	await fire("tool_execution_start", {
		toolCallId: "denied-call",
		toolName: "secret_tool",
		args: { body: secrets[5] },
	}, ctx);
	await fire("tool_execution_end", {
		toolCallId: "denied-call",
		toolName: "secret_tool",
		result: { content: secrets[6] },
		isError: false,
	}, ctx);
	await new Promise((resolve) => setTimeout(resolve, 80));

	const spoolBytes = diskWrites.join("\n");
	const networkBytes = JSON.stringify(mock.delivered);
	for (const secret of secrets) {
		const digest = createHash("sha256").update(secret).digest("hex");
		assert.ok(!spoolBytes.includes(secret), `secret reached disk spool: ${secret}`);
		assert.ok(!networkBytes.includes(secret), `secret reached network: ${secret}`);
		assert.ok(!spoolBytes.includes(digest), `secret hash reached disk spool: ${digest}`);
		assert.ok(!networkBytes.includes(digest), `secret hash reached network: ${digest}`);
	}
	for (const accessKeyID of awsAccessKeyIDs) {
		const leakedSuffix = `[REDACTED]${accessKeyID.slice(-4)}`;
		assert.ok(!spoolBytes.includes(leakedSuffix), `AWS access key suffix reached disk spool: ${leakedSuffix}`);
		assert.ok(!networkBytes.includes(leakedSuffix), `AWS access key suffix reached network: ${leakedSuffix}`);
	}

	const allowed = mock.delivered.find((event) => event.tool_call_id === "allowed-call");
	assert.equal(allowed?.tool_name, "bash");
	assert.equal(allowed?.redacted, true);
	assert.equal(allowed?.tool_input_redacted, true);
	assert.match(allowed?.tool_input ?? "", /\[REDACTED\]/);

	const deniedUse = mock.delivered.find((event) => event.type === "tool_use" && event.tool_call_id === "denied-call");
	const deniedResult = mock.delivered.find((event) => event.type === "tool_result" && event.tool_call_id === "denied-call");
	assert.deepEqual(
		{ id: deniedUse?.tool_call_id, name: deniedUse?.tool_name, body: deniedUse?.tool_input, omitted: deniedUse?.tool_input_omitted },
		{ id: "denied-call", name: "secret_tool", body: undefined, omitted: true },
	);
	assert.deepEqual(
		{ id: deniedResult?.tool_call_id, name: deniedResult?.tool_name, body: deniedResult?.tool_result, omitted: deniedResult?.tool_result_omitted },
		{ id: "denied-call", name: "secret_tool", body: undefined, omitted: true },
	);
	assert.equal(deniedUse?.omitted, true);
	assert.equal(deniedResult?.omitted, true);

	mock.restore();
	console.log("PASS: secrets are redacted before spooling and denied tool bodies are explicitly omitted");
}

async function testReasoningAndNonTextExclusion() {
	const { api, fire } = makeFakePi();
	const mock = installMockFetch({});
	configureTelemetryTest();
	const mod = await import("./index.ts");
	mod.default(api as any);

	const ctx = makeCtx();
	await fire("session_start", {}, ctx);
	await fire(
		"message_end",
		{
			message: {
				role: "assistant",
				stopReason: "stop",
				content: [
					{ type: "thinking", text: "internal chain of thought, should never ship" },
					{ type: "reasoning", text: "also internal" },
					{ type: "tool_use", id: "call-1", name: "bash", input: { cmd: "echo hi" } },
					{ type: "image", data: "base64garbage==", mimeType: "image/png" },
					{ type: "text", text: "Here is the final answer." },
					{ type: "some_future_block_type", payload: { anything: true } },
				],
			},
		},
		ctx,
	);
	await new Promise((resolve) => setTimeout(resolve, 50));

	const finalMessage = mock.delivered.find((d) => d.type === "final_message");
	assert.ok(finalMessage, "expected a final_message event to be delivered");
	assert.equal(finalMessage.content, "Here is the final answer.");
	for (const forbidden of ["internal chain of thought", "also internal", "base64garbage", "some_future_block_type"]) {
		assert.ok(
			!finalMessage.content.includes(forbidden),
			`final_message.content must not include ${JSON.stringify(forbidden)}, got: ${finalMessage.content}`,
		);
	}

	mock.restore();
	console.log("PASS: final_message excludes thinking/reasoning, tool-call, image, and unknown blocks");
}

async function testNoEmptyFinalMessageForNonTextOnlyResponses() {
	const { api, fire } = makeFakePi();
	const mock = installMockFetch({});
	configureTelemetryTest();
	const mod = await import("./index.ts");
	mod.default(api as any);

	const ctx = makeCtx();
	await fire("session_start", {}, ctx);

	// Reasoning-only assistant response: no plain-text block at all.
	await fire(
		"message_end",
		{
			message: {
				role: "assistant",
				content: [{ type: "thinking", text: "internal chain of thought only" }],
				usage: { input: 10, output: 5, cacheRead: 0, cacheWrite: 0, cost: { total: 0.01 } },
			},
		},
		ctx,
	);

	// Tool-call-only assistant response: no plain-text block at all. The
	// stop reason remains turn evidence, but must not promote this to a
	// delivered assistant outcome.
	await fire(
		"message_end",
		{
			message: {
				role: "assistant",
				stopReason: "toolUse",
				content: [{ type: "tool_use", id: "call-2", name: "bash", input: { cmd: "echo hi" } }],
				usage: { input: 3, output: 1, cacheRead: 0, cacheWrite: 0, cost: { total: 0.002 } },
			},
		},
		ctx,
	);

	// Streaming deltas are intentionally not registered telemetry inputs.
	// Even realistic mixed private/non-text delta content must capture nothing.
	await fire("message_update", {
		delta: [
			{ type: "reasoning", text: "private delta" },
			{ type: "image", data: "base64 delta" },
			{ type: "text", text: "unfinished delta" },
		],
	}, ctx);

	await new Promise((resolve) => setTimeout(resolve, 50));

	const finalMessages = mock.delivered.filter((d) => d.type === "final_message");
	assert.equal(
		finalMessages.length,
		0,
		`expected no final_message events for reasoning-only/tool-call-only responses, got: ${JSON.stringify(finalMessages)}`,
	);

	const messageEnds = mock.delivered.filter((d) => d.type === "message_end");
	assert.equal(
		messageEnds.length,
		2,
		`expected message_end (with usage) for both non-text-only responses, got: ${JSON.stringify(messageEnds)}`,
	);
	for (const me of messageEnds) {
		assert.ok(me.usage, `expected message_end to carry usage, got: ${JSON.stringify(me)}`);
	}
	assert.equal(messageEnds[1].stop_reason, "toolUse");
	const serialized = JSON.stringify(mock.delivered);
	for (const forbidden of ["private delta", "base64 delta", "unfinished delta"]) {
		assert.ok(!serialized.includes(forbidden), `delta/private payload was captured: ${forbidden}`);
	}

	mock.restore();
	console.log("PASS: no empty final_message for reasoning-only/tool-call-only responses, message_end still sent");
}

async function testUserMessageBeforeAssistantOrder() {
	const { api, fire } = makeFakePi();
	const mock = installMockFetch({});
	configureTelemetryTest();
	const mod = await import("./index.ts");
	mod.default(api as any);

	const ctx = makeCtx();
	await fire("session_start", {}, ctx);
	await fire(
		"message_end",
		{ message: { role: "user", content: "What files changed?" } },
		ctx,
	);
	await fire(
		"message_end",
		{
			message: {
				role: "assistant",
				content: [{ type: "text", text: "Here is the answer." }],
				stopReason: "stop",
				usage: { input: 4, output: 2, cacheRead: 0, cacheWrite: 0, cost: { total: 0.001 } },
			},
		},
		ctx,
	);
	await new Promise((resolve) => setTimeout(resolve, 50));

	const finalMessages = mock.delivered.filter((d) => d.type === "final_message");
	assert.deepEqual(
		finalMessages.map((d) => ({ role: d.role, content: d.content })),
		[
			{ role: "user", content: "What files changed?" },
			{ role: "assistant", content: "Here is the answer." },
		],
		`expected user final_message before assistant final_message, got: ${JSON.stringify(finalMessages)}`,
	);

	// Assistant usage/privacy behavior is unchanged: exactly one message_end
	// (usage) row, and it is never emitted for the user turn.
	const messageEnds = mock.delivered.filter((d) => d.type === "message_end");
	assert.equal(
		messageEnds.length,
		1,
		`expected exactly one message_end (assistant usage only), got: ${JSON.stringify(messageEnds)}`,
	);
	assert.ok(messageEnds[0].usage, "expected assistant message_end to carry usage");
	assert.equal(messageEnds[0].role, undefined, "message_end DTO carries no role field");

	mock.restore();
	console.log("PASS: user final_message precedes assistant final_message, assistant usage/privacy unchanged");
}

async function testEmptyOrNonTextUserMessageExcluded() {
	const { api, fire } = makeFakePi();
	const mock = installMockFetch({});
	configureTelemetryTest();
	const mod = await import("./index.ts");
	mod.default(api as any);

	const ctx = makeCtx();
	await fire("session_start", {}, ctx);

	// Empty string content.
	await fire("message_end", { message: { role: "user", content: "" } }, ctx);
	// Whitespace-only text block still counts as non-empty text (length > 0
	// after join), so use a genuinely empty array instead to exercise the
	// no-plain-text-block case.
	await fire("message_end", { message: { role: "user", content: [] } }, ctx);
	// Image-only content: no plain-text block.
	await fire(
		"message_end",
		{ message: { role: "user", content: [{ type: "image", data: "base64garbage==", mimeType: "image/png" }] } },
		ctx,
	);

	await new Promise((resolve) => setTimeout(resolve, 50));

	const finalMessages = mock.delivered.filter((d) => d.type === "final_message");
	assert.equal(
		finalMessages.length,
		0,
		`expected no final_message for empty/non-text user content, got: ${JSON.stringify(finalMessages)}`,
	);

	const messageEnds = mock.delivered.filter((d) => d.type === "message_end");
	assert.equal(
		messageEnds.length,
		0,
		`expected no message_end (usage) rows for user messages, got: ${JSON.stringify(messageEnds)}`,
	);

	mock.restore();
	console.log("PASS: empty/non-text user messages produce no final_message and no usage row");
}

async function testSemanticEventCorrelation() {
	const root = await fs.mkdtemp(join(tmpdir(), "foundry-telemetry-semantic-"));
	const parentFile = join(root, "parent.jsonl");
	await fs.writeFile(parentFile, `${JSON.stringify({ type: "session", version: 3, id: "parent-source-uuid", cwd: root })}\n`);
	const mock = installMockFetch({});
	try {
		const { api, fire } = makeFakePi();
		configureTelemetryTest();
		process.env.FOUNDRY_TELEMETRY_TRUSTED_ROOTS = root;
		const mod = await import("./index.ts");
		mod.default(api as any);
		const ctx = makeCtx({
			cwd: root,
			mode: "rpc",
			thinkingLevel: "low",
			sessionManager: {
				getSessionId: () => "semantic-session",
				getHeader: () => ({ parentSession: parentFile }),
			},
		});

		await fire("session_start", {}, ctx);
		for (const [id, text, source] of [
			["user-rpc", "Synthetic Cerberus phase prompt", "rpc"],
			["user-tui", "Typed in Pi", "interactive"],
			["user-ext", "Injected by extension", "extension"],
		] as const) {
			await fire("input", { text, source }, ctx);
			await fire("message_end", { message: { id, role: "user", content: text, timestamp: 1_700_000_000_000 } }, ctx);
		}

		await fire("turn_start", { turnIndex: 7, timestamp: 1_700_000_000_100 }, ctx);
		await fire("message_end", {
			message: {
				id: "assistant-intermediate",
				role: "assistant",
				provider: "acme",
				model: "model-1",
				stopReason: "toolUse",
				content: [{ type: "text", text: "I will inspect that first." }, { type: "toolCall", id: "call-1" }],
				usage: { input: 3, output: 2, cacheRead: 0, cacheWrite: 0, cost: { total: 0.001 } },
			},
		}, ctx);
		await fire("turn_end", { turnIndex: 7 }, ctx);
		for (const [turnIndex, id, stopReason, text] of [
			[8, "assistant-aborted", "aborted", "Partial text before cancellation."],
			[9, "assistant-error", "error", "Partial text before provider failure."],
		] as const) {
			await fire("turn_start", { turnIndex }, ctx);
			await fire("message_end", {
				message: {
					id, role: "assistant", provider: "acme", model: "model-1", stopReason,
					content: [{ type: "text", text }],
					usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, cost: { total: 0.0001 } },
				},
			}, ctx);
			await fire("turn_end", { turnIndex }, ctx);
		}
		await fire("model_select", { model: { provider: "next", id: "model-2" } }, { ...ctx, thinkingLevel: "medium" });
		await fire("thinking_level_select", { level: "high" }, ctx);
		await fire("turn_start", { turnIndex: 10, timestamp: 1_700_000_000_200 }, ctx);
		await fire("message_end", {
			message: {
				id: "assistant-final",
				role: "assistant",
				provider: "next",
				model: "model-2",
				stopReason: "stop",
				content: [{ type: "text", text: "Delivered outcome." }],
				usage: { input: 5, output: 3, cacheRead: 1, cacheWrite: 0, cost: { total: 0.002 } },
			},
		}, ctx);
		await fire("turn_end", { turnIndex: 10 }, ctx);
		await new Promise((resolve) => setTimeout(resolve, 80));

		const start = mock.delivered.find((event) => event.type === "session_start");
		assert.equal(start?.repo_path, undefined, "non-Git cwd was incorrectly recorded as a repository root");
		assert.equal(start?.parent_source_session_id, "parent-source-uuid");
		assert.equal(start?.schema_version, "1");

		const users = mock.delivered.filter((event) => event.type === "final_message" && event.role === "user");
		assert.deepEqual(users.map((event) => [event.source_message_id, event.input_source, event.is_final]), [
			["user-rpc", "harness", false],
			["user-tui", "interactive", false],
			["user-ext", "extension", false],
		]);
		const assistantFinals = mock.delivered.filter((event) => event.type === "final_message" && event.role === "assistant");
		assert.deepEqual(assistantFinals.map((event) => [event.source_message_id, event.turn_index, event.content, event.is_final]), [
			["assistant-final", 10, "Delivered outcome.", true],
		]);
		const turns = mock.delivered.filter((event) => event.type === "message_end");
		assert.deepEqual(turns.map((event) => [event.turn_index, event.source_message_id, event.stop_reason]), [
			[7, "assistant-intermediate", "toolUse"],
			[8, "assistant-aborted", "aborted"],
			[9, "assistant-error", "error"],
			[10, "assistant-final", "stop"],
		]);
		assert.deepEqual(
			{ model: turns[3]?.model, provider: turns[3]?.provider, thinking: turns[3]?.thinking_level },
			{ model: "next/model-2", provider: "next", thinking: "high" },
		);
		assert.ok(turns[0].client_seq < turns[1].client_seq && turns[1].client_seq < turns[2].client_seq && turns[2].client_seq < turns[3].client_seq,
			"assistant turn correlation order was not preserved");
		await fire("session_shutdown", { reason: "quit" }, ctx);
	} finally {
		mock.restore();
		await fs.rm(root, { recursive: true, force: true });
	}
	console.log("PASS: semantic IDs, input provenance, terminal outcomes, model switches, parents, and non-Git cwd");
}

async function testShutdownStopsOldRuntimeBeforeReload() {
	const oldPi = makeFakePi();
	const basePath = configureTelemetryTest();
	const sourceSessionId = "session-abc";
	const spoolPath = sessionSpoolPath(basePath, sourceSessionId);
	const producerId = sessionProducerId("pi:test-host", sourceSessionId);
	const oldFetchCalled = deferred<void>();
	const oldFetchAborted = deferred<void>();
	const originalFetch = globalThis.fetch;

	// Deliberately turn abort into a successful response. A worker that merely
	// aborts fetch, but does not guard acknowledgement after stop, would compact
	// session_start during shutdown and make this test fail deterministically.
	globalThis.fetch = (async (_url: string, init: any) => {
		const events = JSON.parse(init.body as string).events as any[];
		oldFetchCalled.resolve();
		await new Promise<void>((resolve) => {
			const signal = init.signal as AbortSignal;
			if (signal.aborted) resolve();
			else signal.addEventListener("abort", () => resolve(), { once: true });
		});
		oldFetchAborted.resolve();
		return {
			ok: true,
			status: 200,
			json: async () => ({
				results: events.map((event, index) => ({ index, event_id: event.event_id, status: "accepted" })),
			}),
		} as Response;
	}) as typeof fetch;

	const mod = await import("./index.ts");
	mod.default(oldPi.api as any);
	const ctx = makeCtx();
	await oldPi.fire("session_start", {}, ctx);
	await oldFetchCalled.promise;
	await oldPi.fire("session_shutdown", {}, ctx);
	await oldFetchAborted.promise;

	const afterShutdown = new DiskSpool({ path: spoolPath, producerId });
	await afterShutdown.ready();
	assert.deepEqual(
		(await afterShutdown.peek(10)).map((record) => record.event.type),
		["session_start", "session_end"],
		"old runtime acknowledged or compacted after shutdown began",
	);

	// Start a replacement against exactly the same source-session spool. Its
	// request remains blocked, giving the old runtime ample opportunity to race
	// if shutdown had returned before its worker actually exited.
	const newPi = makeFakePi();
	const replacementCalled = deferred<void>();
	globalThis.fetch = (async (_url: string, init: any) => {
		replacementCalled.resolve();
		await new Promise<never>((_resolve, reject) => {
			const signal = init.signal as AbortSignal;
			const abort = () => reject(new Error("replacement fetch aborted"));
			if (signal.aborted) abort();
			else signal.addEventListener("abort", abort, { once: true });
		});
	}) as typeof fetch;
	mod.default(newPi.api as any);
	await newPi.fire("session_start", {}, ctx);
	await replacementCalled.promise;
	await new Promise((resolve) => setTimeout(resolve, 0));

	const duringReload = new DiskSpool({ path: spoolPath, producerId });
	await duringReload.ready();
	assert.deepEqual(
		(await duringReload.peek(10)).map((record) => record.event.type),
		["session_start", "session_end", "session_start"],
		"old runtime mutated the replacement runtime's spool after shutdown returned",
	);

	await newPi.fire("session_shutdown", {}, ctx);
	globalThis.fetch = originalFetch;
	console.log("PASS: shutdown stops the old worker before same-session reload overlap");
}

async function testConcurrentSessionSpoolIsolation() {
	const root = await fs.mkdtemp(join(tmpdir(), "foundry-telemetry-sessions-"));
	try {
		const basePath = join(root, "events.jsonl");
		const producerA = sessionProducerId("pi:test-host", "source-a");
		const producerB = sessionProducerId("pi:test-host", "source-b");
		const pathA = sessionSpoolPath(basePath, "source-a");
		const pathB = sessionSpoolPath(basePath, "source-b");
		assert.notEqual(producerA, producerB);
		assert.notEqual(pathA, pathB);

		const spoolA = new DiskSpool({ path: pathA, producerId: producerA });
		const spoolB = new DiskSpool({ path: pathB, producerId: producerB });
		await Promise.all([
			spoolA.append({ type: "session_start", session: "pi:source-a" }),
			spoolB.append({ type: "session_start", session: "pi:source-b" }),
		]);
		await Promise.all([
			spoolA.append({ type: "tool_use", session: "pi:source-a" }),
			spoolB.append({ type: "tool_use", session: "pi:source-b" }),
		]);

		// This was the acknowledgement-overwrite race with a global file/tmp.
		await Promise.all([
			spoolA.acknowledgeThrough(1),
			spoolB.append({ type: "tool_result", session: "pi:source-b" }),
		]);
		assert.deepEqual((await spoolA.peek(10)).map((record) => record.seq), [2]);
		assert.deepEqual((await spoolB.peek(10)).map((record) => record.seq), [1, 2, 3]);

		// Restart recovery remains stable and source-local.
		const recoveredA = new DiskSpool({ path: pathA, producerId: producerA });
		const recoveredB = new DiskSpool({ path: pathB, producerId: producerB });
		await Promise.all([recoveredA.ready(), recoveredB.ready()]);
		assert.deepEqual((await recoveredA.peek(10)).map((record) => record.event.producer_id), [producerA]);
		assert.deepEqual(
			(await recoveredB.peek(10)).map((record) => record.event.producer_id),
			[producerB, producerB, producerB],
		);
		const [nextA, nextB] = await Promise.all([
			recoveredA.append({ type: "session_end", session: "pi:source-a" }),
			recoveredB.append({ type: "session_end", session: "pi:source-b" }),
		]);
		assert.equal(nextA?.client_seq, 3);
		assert.equal(nextB?.client_seq, 4);
		assert.equal(nextA?.event_id, `${producerA}:3`);
		assert.equal(nextB?.event_id, `${producerB}:4`);
	} finally {
		await fs.rm(root, { recursive: true, force: true });
	}
	console.log("PASS: concurrent source sessions have isolated identity, compaction, and restart recovery");
}

let finalSentinelReached = false;

async function main() {
	await testSessionStartTrustAndAuthenticationConfiguration();
	await testPartialBatchAcknowledgementAndDuplicateReplay();
	await testCrashRestartSpoolRecovery();
	await testSpoolQueueBounds();
	await testHandlerWaitsForAppendDurability();
	await testNetworkOutageDoesNotDelayHandler();
	await testImmediateRestartRecoversHandlerEvent();
	await testShutdownStopsOldRuntimeBeforeReload();
	await testConcurrentSessionSpoolIsolation();
	await testOrderingAndFailureContinuation();
	await testPreSpoolSecretRedactionAndToolBodyDenylist();
	await testReasoningAndNonTextExclusion();
	await testNoEmptyFinalMessageForNonTextOnlyResponses();
	await testUserMessageBeforeAssistantOrder();
	await testEmptyOrNonTextUserMessageExcluded();
	await testSemanticEventCorrelation();
	console.log("PASS: all foundry-telemetry delivery/evidence checks passed");
	finalSentinelReached = true;
}

// A pending Promise does not keep Node alive. Fail explicitly if a test awaits
// one with no active handles instead of silently exiting zero mid-suite.
process.once("beforeExit", () => {
	if (!finalSentinelReached) {
		console.error("FAIL: test process exited before the final completion sentinel");
		process.exitCode = 1;
	}
});

main().catch((err) => {
	console.error("FAIL:", err);
	process.exitCode = 1;
});
