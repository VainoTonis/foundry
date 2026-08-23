/**
 * foundry-telemetry
 *
 * Pi extension that emits best-effort, producer-neutral telemetry to a
 * Foundry server's `POST /api/telemetry/events` endpoint (see
 * internal/httpserver/telemetry_events.go and internal/telemetry/ingest.go).
 *
 * Design constraints (see README.md for the full rationale):
 *   - Type-only dependency on @earendil-works/pi-coding-agent. No runtime
 *     import from pi packages or npm packages, so this file runs under
 *     jiti with zero `npm install` step.
 *   - Never blocks or mutates agent behavior. Every event handler is
 *     fire-and-forget; none of them return a value pi could act on, and
 *     none of them throw.
 *   - Network waits are bounded. Every request carries a timeout via
 *     AbortSignal.timeout(); session_shutdown additionally bounds the
 *     total time spent draining in-flight requests.
 *   - Best effort: any failure (bad URL, network error, non-2xx response,
 *     serialization error) is swallowed, counted, and surfaced only via
 *     the `/foundry-telemetry` status command.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const DEFAULT_FOUNDRY_TELEMETRY_URL = "http://localhost:8080/api/telemetry/events";
const REQUEST_TIMEOUT_MS = 2000;
const SHUTDOWN_DRAIN_TIMEOUT_MS = 1500;
const ORIGIN = "pi-coding-agent";
const MAX_TRACKED_TOOL_CALLS = 500;

type TelemetryUsageDTO = {
	input_tokens: number;
	output_tokens: number;
	cache_read_tokens: number;
	cache_write_tokens: number;
	cost_usd: number;
};

type TelemetryEventDTO = {
	type: "session_start" | "session_end" | "tool_use" | "tool_result" | "message_end" | "final_message";
	session: string;
	source_session_id?: string;
	origin?: string;
	kind?: string;
	repo_path?: string;
	model?: string;
	parent_session?: string;
	tool_call_id?: string;
	tool_name?: string;
	tool_input?: string;
	tool_result?: string;
	is_error?: boolean;
	duration_ms?: number;
	role?: string;
	content?: string;
	usage?: TelemetryUsageDTO;
	timestamp?: string;
};

/** Resolve the telemetry endpoint. Full endpoint URL, not just a host. */
function resolveEndpoint(): string {
	const fromEnv = process.env.FOUNDRY_TELEMETRY_URL?.trim();
	return fromEnv && fromEnv.length > 0 ? fromEnv : DEFAULT_FOUNDRY_TELEMETRY_URL;
}

/** Deterministic JSON serialization (sorted object keys) for evidence fields. */
function stableSerialize(value: unknown): string {
	try {
		return JSON.stringify(sortKeysDeep(value));
	} catch {
		try {
			return String(value);
		} catch {
			return "";
		}
	}
}

function sortKeysDeep(value: unknown): unknown {
	if (Array.isArray(value)) return value.map(sortKeysDeep);
	if (value !== null && typeof value === "object") {
		const out: Record<string, unknown> = {};
		for (const key of Object.keys(value as Record<string, unknown>).sort()) {
			out[key] = sortKeysDeep((value as Record<string, unknown>)[key]);
		}
		return out;
	}
	return value;
}

/** Join text content blocks into a single deterministic evidence string. */
function textFromContent(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return content == null ? "" : stableSerialize(content);
	const parts: string[] = [];
	for (const block of content) {
		if (block && typeof block === "object" && "type" in block) {
			const b = block as { type: string; text?: string };
			if (b.type === "text" && typeof b.text === "string") {
				parts.push(b.text);
				continue;
			}
			if (b.type === "image") {
				parts.push("[image]");
				continue;
			}
		}
		parts.push(stableSerialize(block));
	}
	return parts.join("\n");
}

/**
 * Extract only plain-text evidence from an assistant message's final
 * content, for the `final_message` telemetry event.
 *
 * Unlike `textFromContent` (used for tool call/result evidence, which
 * should faithfully serialize whatever the tool produced), assistant
 * `final_message` evidence must exclude:
 *   - thinking/reasoning blocks (internal chain-of-thought, not the
 *     delivered reply, and potentially sensitive)
 *   - tool-call blocks (tool_use/tool_call; captured separately by the
 *     tool_use/tool_result events)
 *   - image bytes (never forwarded, even as a placeholder — final_message
 *     is text evidence only)
 *   - any other non-text block of unrecognized shape
 *
 * Only blocks with `type === "text"` (or a plain string body) contribute.
 */
function finalMessageText(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	const parts: string[] = [];
	for (const block of content) {
		if (block && typeof block === "object" && "type" in block) {
			const b = block as { type: string; text?: string };
			if (b.type === "text" && typeof b.text === "string") {
				parts.push(b.text);
			}
		}
		// Anything else — thinking/reasoning, tool_use/tool_call, image,
		// or an unrecognized block shape — is intentionally dropped.
	}
	return parts.join("\n");
}

interface TelemetryState {
	endpoint: string;
	sessionKey: string | undefined;
	sourceSessionId: string | undefined;
	sent: number;
	failed: number;
	pending: number;
	lastError: string | undefined;
	lastEventAt: string | undefined;
	lastEventType: string | undefined;
	toolStartedAt: Map<string, number>;
}

function newState(): TelemetryState {
	return {
		endpoint: resolveEndpoint(),
		sessionKey: undefined,
		sourceSessionId: undefined,
		sent: 0,
		failed: 0,
		pending: 0,
		lastError: undefined,
		lastEventAt: undefined,
		lastEventType: undefined,
		toolStartedAt: new Map(),
	};
}

export default function (pi: ExtensionAPI) {
	let state = newState();
	// Tracks in-flight sends so session_shutdown can bound the drain wait
	// without blocking indefinitely on a slow or unreachable endpoint.
	const inFlight: Set<Promise<void>> = new Set();
	// Tail of the serialized send queue: every telemetry POST is chained
	// onto this promise so requests are issued to `fetch` strictly in the
	// order events were emitted (session_start -> activity -> session_end),
	// regardless of how fast/slow any individual network call resolves.
	let queueTail: Promise<void> = Promise.resolve();

	function trackToolStart(toolCallId: string | undefined) {
		if (!toolCallId) return;
		if (state.toolStartedAt.size >= MAX_TRACKED_TOOL_CALLS) {
			// Best-effort bound on unbounded growth if tool_execution_end never
			// arrives for some call (e.g. process crash mid-tool). Drop the
			// oldest entry deterministically (insertion order of a Map).
			const oldest = state.toolStartedAt.keys().next();
			if (!oldest.done) state.toolStartedAt.delete(oldest.value);
		}
		state.toolStartedAt.set(toolCallId, Date.now());
	}

	function takeToolDurationMs(toolCallId: string | undefined): number | undefined {
		if (!toolCallId) return undefined;
		const startedAt = state.toolStartedAt.get(toolCallId);
		if (startedAt === undefined) return undefined;
		state.toolStartedAt.delete(toolCallId);
		return Math.max(0, Date.now() - startedAt);
	}

	/**
	 * Perform one telemetry POST with a bounded timeout. Never throws;
	 * always resolves. This is the only place that calls `fetch`, and it is
	 * only ever invoked from `enqueueSend` below so that requests reach the
	 * network in the same order events were enqueued.
	 */
	async function performSend(dto: TelemetryEventDTO): Promise<void> {
		try {
			const res = await fetch(state.endpoint, {
				method: "POST",
				headers: { "content-type": "application/json" },
				body: JSON.stringify(dto),
				signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
			});
			if (!res.ok) {
				state.failed += 1;
				state.lastError = `HTTP ${res.status} from ${state.endpoint}`;
			} else {
				state.sent += 1;
			}
		} catch (err) {
			// Fail-open: a network error, timeout, or non-2xx response never
			// propagates. It is only recorded for `/foundry-telemetry`, and the
			// queue continues on to the next enqueued event.
			state.failed += 1;
			state.lastError = err instanceof Error ? err.message : String(err);
		} finally {
			state.pending = Math.max(0, state.pending - 1);
			state.lastEventAt = new Date().toISOString();
			state.lastEventType = dto.type;
		}
	}

	/**
	 * Enqueue a telemetry POST onto the serialized send queue. Every call
	 * chains onto `queueTail`, so sends are issued to `fetch` strictly in
	 * enqueue order (session_start, then activity, then session_end) even
	 * though `performSend` never throws and a slow/failed request never
	 * blocks the *next* enqueue from being accepted — it only delays when
	 * that next request actually goes out on the wire.
	 *
	 * Returns the promise for this specific send so `session_shutdown` can
	 * bound its wait on the drain of the whole queue.
	 */
	function enqueueSend(dto: TelemetryEventDTO): Promise<void> {
		state.pending += 1;
		const previous = queueTail;
		const task = previous.then(
			() => performSend(dto),
			() => performSend(dto),
		);
		queueTail = task;
		inFlight.add(task);
		task.finally(() => inFlight.delete(task));
		return task;
	}

	/** Fire-and-forget wrapper: never awaited by event handlers themselves. */
	function emit(dto: TelemetryEventDTO): void {
		void enqueueSend(dto);
	}

	function baseAttribution(ctx: { cwd: string; model?: { provider?: string; id?: string } }) {
		return {
			repo_path: ctx.cwd,
			model: ctx.model ? `${ctx.model.provider ?? "unknown"}/${ctx.model.id ?? "unknown"}` : undefined,
		};
	}

	pi.on("session_start", async (_event, ctx) => {
		try {
			const sessionId = ctx.sessionManager.getSessionId();
			state.sessionKey = `pi:${sessionId}`;
			state.sourceSessionId = sessionId;
			state.endpoint = resolveEndpoint();

			const header = ctx.sessionManager.getHeader?.();
			const parentSession =
				header && typeof header === "object" && "parentSession" in header
					? (header as { parentSession?: string }).parentSession
					: undefined;

			emit({
				type: "session_start",
				session: state.sessionKey,
				source_session_id: state.sourceSessionId,
				origin: ORIGIN,
				kind: ctx.mode,
				parent_session: parentSession,
				timestamp: new Date().toISOString(),
				...baseAttribution(ctx as any),
			});
		} catch {
			// Never let telemetry setup affect session startup.
		}
	});

	pi.on("session_shutdown", async (_event, ctx) => {
		if (!state.sessionKey) return;
		// Enqueue session_end onto the same serialized queue as every other
		// event, so it is only ever sent after all previously enqueued
		// activity (session_start, tool_use/tool_result, message_end,
		// final_message) — `performSend` never throws, so this never rejects.
		enqueueSend({
			type: "session_end",
			session: state.sessionKey,
			timestamp: new Date().toISOString(),
		});

		// Bound the drain of the whole queue (including the session_end send
		// just enqueued) so shutdown never hangs on an unreachable Foundry
		// endpoint. Fail-open: on timeout we simply stop waiting.
		const drain = Promise.allSettled([...inFlight]);
		const timeout = new Promise<void>((resolve) => setTimeout(resolve, SHUTDOWN_DRAIN_TIMEOUT_MS));
		await Promise.race([drain, timeout]);
	});

	pi.on("message_end", async (event, ctx) => {
		try {
			if (!state.sessionKey) return;
			const role = event.message.role;
			if (role !== "assistant" && role !== "user") return;

			const timestamp = new Date().toISOString();

			// Usage/cost rows are only ever derived from assistant turns — user
			// messages carry no model usage and must never produce a
			// `message_end` event.
			if (role === "assistant") {
				const usage = (event.message as { usage?: any }).usage;
				if (usage) {
					emit({
						type: "message_end",
						session: state.sessionKey,
						timestamp,
						usage: {
							input_tokens: usage.input ?? 0,
							output_tokens: usage.output ?? 0,
							cache_read_tokens: usage.cacheRead ?? 0,
							cache_write_tokens: usage.cacheWrite ?? 0,
							cost_usd: usage.cost?.total ?? 0,
						},
						...baseAttribution(ctx as any),
					});
				}
			}

			// final_message evidence: plain-text-only content from a completed
			// assistant or user message. This is the only telemetry a user
			// message ever produces — no usage row, no delta/reasoning/image/
			// tool-result capture, just the finished plain text.
			const finalText = finalMessageText((event.message as { content?: unknown }).content);
			if (finalText.length > 0) {
				emit({
					type: "final_message",
					session: state.sessionKey,
					role,
					content: finalText,
					timestamp,
					...baseAttribution(ctx as any),
				});
			}
		} catch {
			// Never let telemetry serialization affect message finalization.
		}
	});

	pi.on("tool_execution_start", async (event, ctx) => {
		try {
			if (!state.sessionKey) return;
			trackToolStart(event.toolCallId);
			emit({
				type: "tool_use",
				session: state.sessionKey,
				tool_call_id: event.toolCallId,
				tool_name: event.toolName,
				tool_input: stableSerialize(event.args),
				timestamp: new Date().toISOString(),
				...baseAttribution(ctx as any),
			});
		} catch {
			// Never let telemetry affect tool execution.
		}
	});

	pi.on("tool_execution_end", async (event, ctx) => {
		try {
			if (!state.sessionKey) return;
			const durationMs = takeToolDurationMs(event.toolCallId);
			emit({
				type: "tool_result",
				session: state.sessionKey,
				tool_call_id: event.toolCallId,
				tool_name: event.toolName,
				tool_result: textFromContent((event.result as { content?: unknown } | undefined)?.content),
				is_error: Boolean(event.isError),
				duration_ms: durationMs,
				timestamp: new Date().toISOString(),
				...baseAttribution(ctx as any),
			});
		} catch {
			// Never let telemetry affect tool execution.
		}
	});

	pi.registerCommand("foundry-telemetry", {
		description: "Show foundry-telemetry status (endpoint, counts, last error)",
		handler: async (_args, ctx) => {
			const lines = [
				`endpoint: ${state.endpoint}`,
				`session: ${state.sessionKey ?? "(not started)"}`,
				`sent: ${state.sent}  failed: ${state.failed}  pending: ${state.pending}`,
				`last event: ${state.lastEventType ?? "(none)"} @ ${state.lastEventAt ?? "(never)"}`,
				`last error: ${state.lastError ?? "(none)"}`,
			];
			ctx.ui.notify(lines.join("\n"), state.failed > 0 && state.sent === 0 ? "error" : "info");
		},
	});
}
