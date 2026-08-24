import type { DiskSpool, SpoolEvent } from "./spool.ts";

export interface DeliveryClock {
	now(): number;
}

export interface DeliveryScheduler {
	sleep(ms: number): Promise<void>;
}

export interface DeliveryOptions {
	spool: DiskSpool;
	endpoint: () => string;
	bearerToken?: () => string | undefined;
	fetch?: typeof fetch;
	clock?: DeliveryClock;
	scheduler?: DeliveryScheduler;
	batchSize?: number;
	requestTimeoutMs?: number;
	initialBackoffMs?: number;
	maxBackoffMs?: number;
	makeSignal?: (timeoutMs: number) => AbortSignal | undefined;
	onSent?: (count: number, events: SpoolEvent[]) => void;
	onDropped?: (count: number) => void;
	onFailure?: (error: unknown) => void;
}

const realClock: DeliveryClock = { now: () => Date.now() };
const realScheduler: DeliveryScheduler = { sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)) };

/** Ordered, single-worker batch delivery for a DiskSpool. */
export class BatchDelivery {
	private readonly options: DeliveryOptions;
	private readonly stopController = new AbortController();
	private readonly stopWait: Promise<void>;
	private resolveStop!: () => void;
	private worker: Promise<void> | undefined;
	private deadline = 0;
	private wakeVersion = 0;
	private stopped = false;

	constructor(options: DeliveryOptions) {
		this.options = options;
		this.stopWait = new Promise((resolve) => {
			this.resolveStop = resolve;
		});
	}

	/** Start/reawaken delivery, bounded by the supplied wall-clock budget. */
	start(budgetMs: number): void {
		if (this.stopped) return;
		const clock = this.options.clock ?? realClock;
		this.deadline = Math.max(this.deadline, clock.now() + Math.max(0, budgetMs));
		this.wakeVersion += 1;
		if (!this.worker) {
			this.worker = this.run().finally(() => {
				this.worker = undefined;
			});
		}
	}

	/** Wait for empty spool/current worker, but never longer than budgetMs. */
	async drain(budgetMs: number): Promise<void> {
		this.start(budgetMs);
		const scheduler = this.options.scheduler ?? realScheduler;
		const worker = this.worker ?? Promise.resolve();
		await Promise.race([worker, scheduler.sleep(Math.max(0, budgetMs))]);
	}

	/** Permanently stop this worker and wait until it can no longer mutate its spool. */
	async stop(): Promise<void> {
		if (!this.stopped) {
			this.stopped = true;
			this.resolveStop();
			this.stopController.abort();
		}
		await this.worker?.catch(() => undefined);
	}

	private makeRequestSignal(timeoutMs: number): { signal: AbortSignal; cleanup: () => void } {
		const timeoutSignal = this.options.makeSignal
			? this.options.makeSignal(timeoutMs)
			: AbortSignal.timeout(timeoutMs);
		const controller = new AbortController();
		const signals = timeoutSignal ? [this.stopController.signal, timeoutSignal] : [this.stopController.signal];
		const listeners: Array<() => void> = [];
		for (const signal of signals) {
			const abort = () => controller.abort(signal.reason);
			if (signal.aborted) abort();
			else {
				signal.addEventListener("abort", abort, { once: true });
				listeners.push(() => signal.removeEventListener("abort", abort));
			}
		}
		return { signal: controller.signal, cleanup: () => listeners.forEach((remove) => remove()) };
	}

	private async run(): Promise<void> {
		const clock = this.options.clock ?? realClock;
		const scheduler = this.options.scheduler ?? realScheduler;
		let backoff = this.options.initialBackoffMs ?? 250;
		const maxBackoff = this.options.maxBackoffMs ?? 30_000;
		await this.options.spool.ready();

		while (!this.stopped && clock.now() <= this.deadline) {
			const batch = await this.options.spool.peek(this.options.batchSize ?? 100);
			if (this.stopped || batch.length === 0) return;
			const attemptedVersion = this.wakeVersion;
			try {
				const request = this.makeRequestSignal(this.options.requestTimeoutMs ?? 2_000);
				try {
					const bearerToken = this.options.bearerToken?.();
					const response = await (this.options.fetch ?? globalThis.fetch)(this.options.endpoint(), {
						method: "POST",
						headers: {
							"content-type": "application/json",
							...(bearerToken ? { authorization: `Bearer ${bearerToken}` } : {}),
						},
						body: JSON.stringify({ events: batch.map((record) => record.event) }),
						signal: request.signal,
					});
					if (this.stopped) return;
					if (!response.ok) throw new Error(`HTTP ${response.status} from ${this.options.endpoint()}`);

					const body = (await response.json()) as {
						results?: Array<{ index: number; event_id?: string; status: string; error?: string }>;
					};
					if (this.stopped) return;
					if (!Array.isArray(body.results) || body.results.length !== batch.length) {
						throw new Error("invalid telemetry batch acknowledgement");
					}

					let accepted = 0;
					let rejected = 0;
					for (let index = 0; index < batch.length; index += 1) {
						const result = body.results[index];
						if (result.index !== index || (result.event_id && result.event_id !== batch[index].event.event_id)) {
							throw new Error("mismatched telemetry batch acknowledgement");
						}
						if (result.status === "accepted" || result.status === "duplicate") accepted += 1;
						else if (result.status === "rejected") rejected += 1;
						else throw new Error(`unknown telemetry acknowledgement status: ${result.status}`);
					}

					// Compact only after every outcome in this ordered prefix is known.
					if (this.stopped) return;
					await this.options.spool.acknowledgeThrough(batch[batch.length - 1].seq);
					if (this.stopped) return;
					if (accepted) this.options.onSent?.(accepted, batch.map((record) => record.event));
					if (rejected) this.options.onDropped?.(rejected);
					backoff = this.options.initialBackoffMs ?? 250;
				} finally {
					request.cleanup();
				}
			} catch (err) {
				if (this.stopped) return;
				this.options.onFailure?.(err);
				const remaining = this.deadline - clock.now();
				if (remaining <= 0) return;
				await Promise.race([scheduler.sleep(Math.min(backoff, remaining)), this.stopWait]);
				if (this.stopped) return;
				backoff = Math.min(maxBackoff, backoff * 2);
				// A start() while sleeping extends the same worker's deadline.
				if (attemptedVersion !== this.wakeVersion) backoff = this.options.initialBackoffMs ?? 250;
			}
		}
	}
}
