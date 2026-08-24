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
 *   - Durability-first hooks. Every event handler awaits its serialized
 *     local spool append, but network delivery is always fire-and-forget.
 *     Append failures are swallowed and never mutate agent behavior.
 *   - Network waits are bounded in the background. Every request carries
 *     a timeout via AbortSignal.timeout(); no Pi hook awaits a request.
 *   - Best effort: any failure (bad URL, network error, non-2xx response,
 *     serialization error) is swallowed, counted, and surfaced only via
 *     the `/foundry-telemetry` status command.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { execFile } from "node:child_process";
import { open, realpath } from "node:fs/promises";
import { hostname, homedir } from "node:os";
import { delimiter, isAbsolute, join, relative, sep } from "node:path";
import { promisify } from "node:util";
import { BatchDelivery } from "./delivery.ts";
import { DiskSpool, sessionProducerId, sessionSpoolPath } from "./spool.ts";
import type { SpoolFileSystem } from "./spool.ts";

const DEFAULT_FOUNDRY_TELEMETRY_URL = "http://localhost:8080/api/telemetry/events";
const REQUEST_TIMEOUT_MS = 2000;
const STARTUP_DRAIN_TIMEOUT_MS = 5000;
const ORIGIN = "pi-coding-agent";
const MAX_TRACKED_TOOL_CALLS = 500;
const execFileAsync = promisify(execFile);

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
	parent_source_session_id?: string;
	schema_version?: string;
	close_reason?: string;
	tool_call_id?: string;
	tool_name?: string;
	tool_input?: string;
	tool_result?: string;
	is_error?: boolean;
	duration_ms?: number;
	provider?: string;
	thinking_level?: string;
	stop_reason?: string;
	role?: string;
	content?: string;
	source_message_id?: string;
	turn_index?: number;
	input_source?: string;
	is_final?: boolean;
	usage?: TelemetryUsageDTO;
	/** Privacy annotations added before the event reaches the disk spool. */
	redacted?: boolean;
	omitted?: boolean;
	tool_input_redacted?: boolean;
	tool_input_omitted?: boolean;
	tool_result_redacted?: boolean;
	tool_result_omitted?: boolean;
	content_redacted?: boolean;
	timestamp?: string;
	producer_id?: string;
	event_id?: string;
	client_seq?: number;
};

/** Resolve the telemetry endpoint. Full endpoint URL, not just a host. */
function resolveEndpoint(): string {
	const fromEnv = process.env.FOUNDRY_TELEMETRY_URL?.trim();
	return fromEnv && fromEnv.length > 0 ? fromEnv : DEFAULT_FOUNDRY_TELEMETRY_URL;
}

function configuredTrustedRoots(): string[] {
	return (process.env.FOUNDRY_TELEMETRY_TRUSTED_ROOTS ?? "")
		.split(delimiter)
		.map((root) => root.trim())
		.filter(Boolean);
}

async function trustedCwd(cwd: string): Promise<{ trusted: boolean; status: string }> {
	const configured = configuredTrustedRoots();
	if (configured.length === 0) {
		return { trusted: false, status: "disabled: FOUNDRY_TELEMETRY_TRUSTED_ROOTS is not configured" };
	}
	try {
		const [resolvedCwd, roots] = await Promise.all([
			realpath(cwd),
			Promise.all(configured.map(async (root) => realpath(root).catch(() => undefined))),
		]);
		const trusted = roots.some((root) => {
			if (!root) return false;
			const child = relative(root, resolvedCwd);
			return child === "" || (child !== ".." && !child.startsWith(`..${sep}`) && !isAbsolute(child));
		});
		return trusted
			? { trusted: true, status: `enabled: trusted cwd ${resolvedCwd}` }
			: { trusted: false, status: `untrusted: cwd ${resolvedCwd} is outside configured roots` };
	} catch {
		return { trusted: false, status: `untrusted: cwd ${cwd} could not be resolved` };
	}
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

const REDACTED = "[REDACTED]";
const PRIVATE_KEY_PATTERN = /-----BEGIN [^-\r\n]*PRIVATE KEY-----[\s\S]*?-----END [^-\r\n]*PRIVATE KEY-----/gi;
const BEARER_PATTERN = /\bBearer\s+[A-Za-z0-9._~+/=-]{8,}/gi;
const SECRET_ASSIGNMENT_PATTERN = /((?:["']?\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|bearer[_-]?token|token|password|passwd|authorization|client[_-]?secret|private[_-]?key)\b["']?)\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;&}]+)/gi;
const COMMON_TOKEN_PATTERN = /\b(?:sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|xox[baprs]-[A-Za-z0-9-]{8,}|AKIA[A-Z0-9]{16}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})\b/g;

function redactSecrets(value: string): { value: string; redacted: boolean } {
	let sanitized = value.replace(PRIVATE_KEY_PATTERN, REDACTED);
	sanitized = sanitized.replace(BEARER_PATTERN, REDACTED);
	sanitized = sanitized.replace(SECRET_ASSIGNMENT_PATTERN, (_match, prefix: string) => `${prefix}"${REDACTED}"`);
	sanitized = sanitized.replace(COMMON_TOKEN_PATTERN, REDACTED);
	return { value: sanitized, redacted: sanitized !== value };
}

function configuredToolBodyDenylist(options: TelemetryExtensionOptions): Set<string> {
	const configured = [
		...(options.toolBodyDenylist ?? []),
		...(process.env.FOUNDRY_TELEMETRY_TOOL_BODY_DENYLIST ?? "").split(","),
	];
	return new Set(configured.map((name) => name.trim().toLowerCase()).filter(Boolean));
}

/** Last privacy boundary: this must run before DiskSpool.append. */
function preparePayload(dto: TelemetryEventDTO, deniedTools: Set<string>): TelemetryEventDTO {
	const prepared: TelemetryEventDTO = { ...dto };
	const denied = Boolean(dto.tool_name && deniedTools.has(dto.tool_name.toLowerCase()));
	if (denied && dto.type === "tool_use") {
		delete prepared.tool_input;
		prepared.tool_input_omitted = true;
		prepared.omitted = true;
	}
	if (denied && dto.type === "tool_result") {
		delete prepared.tool_result;
		prepared.tool_result_omitted = true;
		prepared.omitted = true;
	}

	for (const [key, value] of Object.entries(prepared)) {
		if (typeof value !== "string") continue;
		const result = redactSecrets(value);
		if (!result.redacted) continue;
		(prepared as Record<string, unknown>)[key] = result.value;
		prepared.redacted = true;
		if (key === "tool_input") prepared.tool_input_redacted = true;
		if (key === "tool_result") prepared.tool_result_redacted = true;
		if (key === "content") prepared.content_redacted = true;
	}
	return prepared;
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
async function resolveGitRoot(cwd: string): Promise<string | undefined> {
	try {
		const { stdout } = await execFileAsync("git", ["-C", cwd, "rev-parse", "--show-toplevel"], {
			timeout: 2_000,
			maxBuffer: 16 * 1024,
		});
		const root = stdout.trim();
		return root ? await realpath(root) : undefined;
	} catch {
		return undefined;
	}
}

async function parentSourceSessionId(parentSession: string | undefined): Promise<string | undefined> {
	if (!parentSession) return undefined;
	let handle: Awaited<ReturnType<typeof open>> | undefined;
	try {
		handle = await open(parentSession, "r");
		const buffer = Buffer.alloc(16 * 1024);
		const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
		const firstLine = buffer.subarray(0, bytesRead).toString("utf8").split("\n", 1)[0];
		const header = JSON.parse(firstLine) as { type?: unknown; id?: unknown };
		return header.type === "session" && typeof header.id === "string" && header.id.length > 0
			? header.id
			: undefined;
	} catch {
		return undefined;
	} finally {
		await handle?.close().catch(() => undefined);
	}
}

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

interface SessionRuntime {
	sourceSessionId: string;
	repoPath: string | undefined;
	spool: DiskSpool;
	delivery: BatchDelivery;
}

interface TelemetryState {
	endpoint: string;
	bearerToken: string | undefined;
	captureStatus: string;
	sessionKey: string | undefined;
	sourceSessionId: string | undefined;
	runtime: SessionRuntime | undefined;
	sent: number;
	failed: number;
	pending: number;
	diskBytes: number;
	dropped: number;
	lastError: string | undefined;
	lastEventAt: string | undefined;
	lastEventType: string | undefined;
	toolStartedAt: Map<string, number>;
	inputSources: Array<{ text: string; source: "interactive" | "harness" | "extension" }>;
	provider: string | undefined;
	model: string | undefined;
	thinkingLevel: string | undefined;
	currentTurnIndex: number | undefined;
	messageCounter: number;
}

function newState(): TelemetryState {
	return {
		endpoint: resolveEndpoint(),
		bearerToken: undefined,
		captureStatus: configuredTrustedRoots().length > 0
			? "disabled: no session started"
			: "disabled: FOUNDRY_TELEMETRY_TRUSTED_ROOTS is not configured",
		sessionKey: undefined,
		sourceSessionId: undefined,
		runtime: undefined,
		sent: 0,
		failed: 0,
		pending: 0,
		diskBytes: 0,
		dropped: 0,
		lastError: undefined,
		lastEventAt: undefined,
		lastEventType: undefined,
		toolStartedAt: new Map(),
		inputSources: [],
		provider: undefined,
		model: undefined,
		thinkingLevel: undefined,
		currentTurnIndex: undefined,
		messageCounter: 0,
	};
}

export interface TelemetryExtensionOptions {
	/** Test seam for deterministic append-durability checks. */
	spoolFileSystem?: SpoolFileSystem;
	/** Tool names whose input and result bodies must never be captured. */
	toolBodyDenylist?: string[];
}

export default function (pi: ExtensionAPI, options: TelemetryExtensionOptions = {}) {
	const state = newState();
	const deniedToolBodies = configuredToolBodyDenylist(options);
	const baseProducerId = process.env.FOUNDRY_TELEMETRY_PRODUCER_ID?.trim() || `pi:${hostname()}`;
	const baseSpoolPath =
		process.env.FOUNDRY_TELEMETRY_SPOOL_PATH?.trim() ||
		join(homedir(), ".pi", "agent", "foundry-telemetry", "events.jsonl");

	async function refreshDiskStats(runtime: SessionRuntime): Promise<void> {
		const stats = await runtime.spool.stats();
		if (state.runtime !== runtime) return;
		state.pending = stats.diskEvents;
		state.diskBytes = stats.diskBytes;
		state.dropped = Math.max(state.dropped, stats.dropped);
	}

	function createRuntime(sourceSessionId: string, repoPath: string | undefined): SessionRuntime {
		const spool = new DiskSpool({
			path: sessionSpoolPath(baseSpoolPath, sourceSessionId),
			producerId: sessionProducerId(baseProducerId, sourceSessionId),
			fs: options.spoolFileSystem,
			maxEvents: Number(process.env.FOUNDRY_TELEMETRY_MAX_EVENTS) || 10_000,
			maxBytes: Number(process.env.FOUNDRY_TELEMETRY_MAX_BYTES) || 16 * 1024 * 1024,
		});
		const runtime = { sourceSessionId, repoPath, spool } as SessionRuntime;
		runtime.delivery = new BatchDelivery({
			spool,
			// Capture the worker's transport; hooks still never await it.
			fetch: globalThis.fetch,
			endpoint: () => state.endpoint,
			bearerToken: () => state.bearerToken,
			requestTimeoutMs: REQUEST_TIMEOUT_MS,
			onSent: (count, events) => {
				state.sent += count;
				const last = events[events.length - 1];
				state.lastEventAt = new Date().toISOString();
				state.lastEventType = typeof last?.type === "string" ? last.type : undefined;
				void refreshDiskStats(runtime);
			},
			onDropped: (count) => {
				state.dropped += count;
				void refreshDiskStats(runtime);
			},
			onFailure: (err) => {
				state.failed += 1;
				state.lastError = err instanceof Error ? err.message : String(err);
			},
		});
		return runtime;
	}

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

	/** Await local durability only; delivery is started but never awaited. */
	async function emit(dto: TelemetryEventDTO): Promise<void> {
		const runtime = state.runtime;
		if (!runtime) return;
		state.pending += 1;
		try {
			const event = await runtime.spool.append(preparePayload(dto, deniedToolBodies));
			if (!event) {
				state.pending = Math.max(0, state.pending - 1);
				state.dropped += 1;
			}
			void refreshDiskStats(runtime);
			// Also drain an already-full recovered spool when this append is dropped.
			runtime.delivery.start(STARTUP_DRAIN_TIMEOUT_MS);
		} catch (err) {
			state.pending = Math.max(0, state.pending - 1);
			state.dropped += 1;
			state.lastError = err instanceof Error ? err.message : String(err);
		}
	}

	function repoAttribution() {
		return { repo_path: state.runtime?.repoPath };
	}

	function currentTurnAttribution() {
		return {
			...repoAttribution(),
			model: state.model,
			provider: state.provider,
			thinking_level: state.thinkingLevel,
		};
	}

	function sourceMessageId(message: Record<string, any>, ctx: any): string {
		if (typeof message.id === "string" && message.id.length > 0) return message.id;
		const entries = ctx.sessionManager.getEntries?.() as any[] | undefined;
		if (entries) {
			for (let index = entries.length - 1; index >= 0; index -= 1) {
				const entry = entries[index];
				if (entry?.type !== "message" || entry.message?.role !== message.role) continue;
				if (entry.message === message || (
					entry.message?.timestamp === message.timestamp &&
					stableSerialize(entry.message?.content) === stableSerialize(message.content)
				)) {
					if (typeof entry.id === "string" && entry.id.length > 0) return entry.id;
				}
			}
		}
		if (typeof message.timestamp === "number" || typeof message.timestamp === "string") {
			return `${state.sourceSessionId ?? "unknown"}:message:${message.role}:${message.timestamp}`;
		}
		state.messageCounter += 1;
		return `${state.sourceSessionId ?? "unknown"}:message:${state.messageCounter}`;
	}

	function fallbackInputSource(mode: string): "interactive" | "harness" {
		return mode === "tui" ? "interactive" : "harness";
	}

	function takeInputSource(content: unknown, mode: string): "interactive" | "harness" | "extension" {
		const text = finalMessageText(content);
		const match = state.inputSources.findIndex((input) => input.text === text);
		if (match >= 0) return state.inputSources.splice(match, 1)[0].source;
		// Skill/template expansion can change the persisted text. Input and user
		// message events remain ordered, so consume the oldest unmatched source.
		return state.inputSources.shift()?.source ?? fallbackInputSource(mode);
	}

	pi.on("session_start", async (_event, ctx) => {
		try {
			state.endpoint = resolveEndpoint();
			state.bearerToken = process.env.FOUNDRY_TELEMETRY_BEARER_TOKEN?.trim() || undefined;
			const trust = await trustedCwd(ctx.cwd);
			state.captureStatus = trust.status;
			if (!trust.trusted) return;

			const sessionId = ctx.sessionManager.getSessionId();
			state.sessionKey = `pi:${sessionId}`;
			state.sourceSessionId = sessionId;
			state.provider = ctx.model?.provider;
			state.model = ctx.model ? `${ctx.model.provider ?? "unknown"}/${ctx.model.id ?? "unknown"}` : undefined;
			state.thinkingLevel = ctx.thinkingLevel ?? pi.getThinkingLevel?.();
			state.inputSources = [];
			state.currentTurnIndex = undefined;
			state.messageCounter = 0;
			const runtime = createRuntime(sessionId, await resolveGitRoot(ctx.cwd));
			state.runtime = runtime;
			void refreshDiskStats(runtime);

			const header = ctx.sessionManager.getHeader?.();
			const parentSession =
				header && typeof header === "object" && "parentSession" in header
					? (header as { parentSession?: string }).parentSession
					: undefined;

			await emit({
				type: "session_start",
				session: state.sessionKey,
				source_session_id: state.sourceSessionId,
				schema_version: "1",
				origin: ORIGIN,
				kind: ctx.mode,
				parent_session: parentSession,
				parent_source_session_id: await parentSourceSessionId(parentSession),
				repo_path: runtime.repoPath,
				model: state.model,
				timestamp: new Date().toISOString(),
			});
		} catch {
			// Never let telemetry setup affect session startup.
		}
	});

	pi.on("session_shutdown", async (event, _ctx) => {
		const runtime = state.runtime;
		if (!state.sessionKey || !runtime) return;
		try {
			// Preserve the durability-first contract: stop only after session_end
			// has joined the serialized spool.
			await emit({
				type: "session_end",
				session: state.sessionKey,
				close_reason: typeof event?.reason === "string" ? event.reason : undefined,
				timestamp: new Date().toISOString(),
			});
		} finally {
			// Pi may load the replacement extension as soon as this hook returns.
			// Do not leave an old worker able to acknowledge that shared spool.
			await runtime.delivery.stop();
			if (state.runtime === runtime) {
				state.runtime = undefined;
				state.sessionKey = undefined;
				state.sourceSessionId = undefined;
			}
		}
	});

	pi.on("input", (event) => {
		if (state.inputSources.length >= 500) state.inputSources.shift();
		const source = event.source === "interactive"
			? "interactive"
			: event.source === "extension"
				? "extension"
				: "harness";
		state.inputSources.push({ text: event.text, source });
	});

	pi.on("model_select", (event, ctx) => {
		state.provider = event.model?.provider;
		state.model = event.model ? `${event.model.provider ?? "unknown"}/${event.model.id ?? "unknown"}` : undefined;
		state.thinkingLevel = ctx.thinkingLevel ?? state.thinkingLevel;
	});

	pi.on("thinking_level_select", (event) => {
		state.thinkingLevel = event.level;
	});

	pi.on("turn_start", (event) => {
		state.currentTurnIndex = event.turnIndex;
	});

	pi.on("turn_end", (event) => {
		if (state.currentTurnIndex === event.turnIndex) state.currentTurnIndex = undefined;
	});

	pi.on("message_end", async (event, ctx) => {
		try {
			if (!state.sessionKey) return;
			const message = event.message as Record<string, any>;
			const role = message.role;
			if (role !== "assistant" && role !== "user") return;

			const timestamp = typeof message.timestamp === "number"
				? new Date(message.timestamp).toISOString()
				: new Date().toISOString();
			const turnIndex = role === "assistant" ? state.currentTurnIndex : undefined;
			const messageId = sourceMessageId(message, ctx);

			// Usage/cost rows are only ever derived from assistant turns — user
			// messages carry no model usage and must never produce a
			// `message_end` event.
			if (role === "assistant") {
				const usage = message.usage;
				if (usage) {
					const provider = typeof message.provider === "string" ? message.provider : state.provider;
					const model = typeof message.model === "string"
						? `${provider ?? "unknown"}/${message.model}`
						: state.model;
					await emit({
						type: "message_end",
						session: state.sessionKey,
						timestamp,
						...currentTurnAttribution(),
						model,
						provider,
						stop_reason: typeof message.stopReason === "string" ? message.stopReason : undefined,
						turn_index: turnIndex,
						source_message_id: messageId,
						usage: {
							input_tokens: usage.input ?? 0,
							output_tokens: usage.output ?? 0,
							cache_read_tokens: usage.cacheRead ?? 0,
							cache_write_tokens: usage.cacheWrite ?? 0,
							cost_usd: usage.cost?.total ?? 0,
						},
					});
				}
			}

			// final_message evidence: plain-text-only content from a completed
			// assistant or user message. This is the only telemetry a user
			// message ever produces — no usage row, no delta/reasoning/image/
			// tool-result capture, just the finished plain text.
			const finalText = finalMessageText(message.content);
			const deliveredTerminalAssistant = role === "assistant" &&
				(message.stopReason === "stop" || message.stopReason === "length");
			if (finalText.length > 0 && (role === "user" || deliveredTerminalAssistant)) {
				await emit({
					type: "final_message",
					session: state.sessionKey,
					role,
					content: finalText,
					source_message_id: messageId,
					turn_index: turnIndex,
					input_source: role === "user" ? takeInputSource(message.content, ctx.mode) : undefined,
					is_final: deliveredTerminalAssistant,
					timestamp,
					...repoAttribution(),
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
			await emit({
				type: "tool_use",
				session: state.sessionKey,
				tool_call_id: event.toolCallId,
				tool_name: event.toolName,
				tool_input: stableSerialize(event.args),
				timestamp: new Date().toISOString(),
				...repoAttribution(),
			});
		} catch {
			// Never let telemetry affect tool execution.
		}
	});

	pi.on("tool_execution_end", async (event, ctx) => {
		try {
			if (!state.sessionKey) return;
			const durationMs = takeToolDurationMs(event.toolCallId);
			await emit({
				type: "tool_result",
				session: state.sessionKey,
				tool_call_id: event.toolCallId,
				tool_name: event.toolName,
				tool_result: textFromContent((event.result as { content?: unknown } | undefined)?.content),
				is_error: Boolean(event.isError),
				duration_ms: durationMs,
				timestamp: new Date().toISOString(),
				...repoAttribution(),
			});
		} catch {
			// Never let telemetry affect tool execution.
		}
	});

	pi.registerCommand("foundry-telemetry", {
		description: "Show foundry-telemetry status (endpoint, counts, last error)",
		handler: async (_args, ctx) => {
			const lines = [
				`capture: ${state.captureStatus}`,
				`endpoint: ${state.endpoint}`,
				`session: ${state.sessionKey ?? "(not started)"}`,
				`sent: ${state.sent}  failed batches: ${state.failed}  pending: ${state.pending}`,
				`disk: ${state.diskBytes} bytes  dropped: ${state.dropped}`,
				`last event: ${state.lastEventType ?? "(none)"} @ ${state.lastEventAt ?? "(never)"}`,
				`last error: ${state.lastError ?? "(none)"}`,
			];
			ctx.ui.notify(lines.join("\n"), state.failed > 0 && state.sent === 0 ? "error" : "info");
		},
	});
}
