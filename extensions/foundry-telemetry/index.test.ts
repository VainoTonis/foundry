/**
 * Mocked-fetch verification for foundry-telemetry's delivery and evidence
 * fidelity guarantees:
 *
 *   1. Ordering: telemetry POSTs are delivered to `fetch` in event order
 *      (session_start before any activity, session_end after a bounded
 *      drain of everything enqueued before it) even when individual
 *      network calls resolve out of order.
 *   2. Failure continuation: a failing/rejecting POST does not stop later
 *      events from being sent (fail-open) and does not break ordering.
 *   3. Reasoning/thinking/tool-call/image exclusion: `final_message`
 *      content only carries plain text blocks.
 *   4. No empty evidence: reasoning-only or tool-call-only assistant
 *      responses (no plain-text block) never produce a `final_message`
 *      event, while the usage-bearing `message_end` event is still sent.
 *   5. User messages: a completed, non-empty plain-text user message is
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

type Handler = (event: any, ctx: any) => Promise<void> | void;

/** Minimal fake ExtensionAPI capturing registered event handlers. */
function makeFakePi() {
	const handlers = new Map<string, Handler[]>();
	return {
		api: {
			on(name: string, handler: Handler) {
				const list = handlers.get(name) ?? [];
				list.push(handler);
				handlers.set(name, list);
			},
			registerCommand(_name: string, _spec: unknown) {
				// not exercised by these tests
			},
		},
		async fire(name: string, event: any, ctx: any) {
			for (const handler of handlers.get(name) ?? []) {
				await handler(event, ctx);
			}
		},
	};
}

function makeCtx(overrides: Record<string, unknown> = {}) {
	return {
		cwd: "/repo",
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
	globalThis.fetch = (async (_url: string, init: any) => {
		const dto = JSON.parse(init.body as string);
		delivered.push(dto);
		const delay = opts.delayByType?.[dto.type] ?? 0;
		if (delay > 0) await new Promise((resolve) => setTimeout(resolve, delay));
		if (opts.failTypes?.has(dto.type)) {
			throw new Error(`simulated failure for ${dto.type}`);
		}
		return { ok: true, status: 200 } as Response;
	}) as typeof fetch;
	return {
		delivered,
		restore() {
			globalThis.fetch = originalFetch;
		},
	};
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

	process.env.FOUNDRY_TELEMETRY_URL = "http://mock.invalid/api/telemetry/events";
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
	await fire("session_shutdown", {}, ctx);

	// Wait past every artificial delay plus the shutdown drain bound so the
	// whole queue has definitely settled before asserting.
	await new Promise((resolve) => setTimeout(resolve, 200));

	const types = mock.delivered.map((d) => d.type);
	assert.deepEqual(
		types,
		["session_start", "tool_use", "tool_result", "session_end"],
		`expected strict event order, got: ${JSON.stringify(types)}`,
	);

	mock.restore();
	console.log("PASS: ordering preserved despite slow session_start and failing tool_use");
}

async function testReasoningAndNonTextExclusion() {
	const { api, fire } = makeFakePi();
	const mock = installMockFetch({});
	process.env.FOUNDRY_TELEMETRY_URL = "http://mock.invalid/api/telemetry/events";
	const mod = await import("./index.ts");
	mod.default(api as any);

	const ctx = makeCtx();
	await fire("session_start", {}, ctx);
	await fire(
		"message_end",
		{
			message: {
				role: "assistant",
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
	process.env.FOUNDRY_TELEMETRY_URL = "http://mock.invalid/api/telemetry/events";
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

	// Tool-call-only assistant response: no plain-text block at all.
	await fire(
		"message_end",
		{
			message: {
				role: "assistant",
				content: [{ type: "tool_use", id: "call-2", name: "bash", input: { cmd: "echo hi" } }],
				usage: { input: 3, output: 1, cacheRead: 0, cacheWrite: 0, cost: { total: 0.002 } },
			},
		},
		ctx,
	);

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

	mock.restore();
	console.log("PASS: no empty final_message for reasoning-only/tool-call-only responses, message_end still sent");
}

async function testUserMessageBeforeAssistantOrder() {
	const { api, fire } = makeFakePi();
	const mock = installMockFetch({});
	process.env.FOUNDRY_TELEMETRY_URL = "http://mock.invalid/api/telemetry/events";
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
	process.env.FOUNDRY_TELEMETRY_URL = "http://mock.invalid/api/telemetry/events";
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

async function main() {
	await testOrderingAndFailureContinuation();
	await testReasoningAndNonTextExclusion();
	await testNoEmptyFinalMessageForNonTextOnlyResponses();
	await testUserMessageBeforeAssistantOrder();
	await testEmptyOrNonTextUserMessageExcluded();
	console.log("PASS: all foundry-telemetry delivery/evidence checks passed");
}

main().catch((err) => {
	console.error("FAIL:", err);
	process.exitCode = 1;
});
