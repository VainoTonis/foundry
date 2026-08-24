import { createHash } from "node:crypto";
import { promises as nodeFs } from "node:fs";
import { dirname, join } from "node:path";

export type SpoolEvent = Record<string, unknown> & {
	producer_id: string;
	event_id: string;
	client_seq: number;
};

export interface SpoolRecord {
	seq: number;
	event: SpoolEvent;
	bytes: number;
}

export interface SpoolFileSystem {
	mkdir(path: string, options?: { recursive?: boolean }): Promise<unknown>;
	readFile(path: string, encoding: "utf8"): Promise<string>;
	writeFile(path: string, data: string, encoding: "utf8"): Promise<void>;
	appendFile(path: string, data: string, encoding: "utf8"): Promise<void>;
	rename(from: string, to: string): Promise<void>;
}

export interface DiskSpoolOptions {
	path: string;
	producerId: string;
	fs?: SpoolFileSystem;
	maxEvents?: number;
	maxBytes?: number;
}

export interface SpoolStats {
	diskEvents: number;
	diskBytes: number;
	dropped: number;
}

function missing(err: unknown): boolean {
	return Boolean(err && typeof err === "object" && "code" in err && (err as { code?: string }).code === "ENOENT");
}

/**
 * Derive stable ownership from Pi's source session. The digest keeps arbitrary
 * session IDs from escaping the spool directory or exceeding filename limits.
 */
export function sessionSpoolPath(basePath: string, sourceSessionId: string): string {
	const digest = createHash("sha256").update(sourceSessionId).digest("hex");
	return join(`${basePath}.sessions`, `${digest}.jsonl`);
}

/** A configured producer ID is a namespace; the source session owns the identity. */
export function sessionProducerId(baseProducerId: string, sourceSessionId: string): string {
	return `${baseProducerId}:session:${sourceSessionId}`;
}

/** A bounded JSONL outbox. All mutations are serialized in call order. */
export class DiskSpool {
	private readonly fs: SpoolFileSystem;
	private readonly path: string;
	private readonly sequencePath: string;
	private readonly producerId: string;
	private readonly maxEvents: number;
	private readonly maxBytes: number;
	private records: SpoolRecord[] = [];
	private nextSeq = 1;
	private tail: Promise<unknown>;
	private droppedCount = 0;

	constructor(options: DiskSpoolOptions) {
		this.fs = options.fs ?? (nodeFs as unknown as SpoolFileSystem);
		this.path = options.path;
		this.sequencePath = `${options.path}.sequence`;
		this.producerId = options.producerId;
		this.maxEvents = options.maxEvents ?? 10_000;
		this.maxBytes = options.maxBytes ?? 16 * 1024 * 1024;
		this.tail = this.recover();
	}

	private async read(path: string): Promise<string> {
		try {
			return await this.fs.readFile(path, "utf8");
		} catch (err) {
			if (missing(err)) return "";
			throw err;
		}
	}

	private async recover(): Promise<void> {
		await this.fs.mkdir(dirname(this.path), { recursive: true });
		const [contents, sequenceText] = await Promise.all([this.read(this.path), this.read(this.sequencePath)]);
		let maximum = Number.parseInt(sequenceText, 10) || 0;
		for (const line of contents.split("\n")) {
			if (!line) continue;
			try {
				const event = JSON.parse(line) as SpoolEvent;
				if (!event || event.producer_id !== this.producerId || !Number.isSafeInteger(event.client_seq)) continue;
				const bytes = Buffer.byteLength(`${line}\n`);
				this.records.push({ seq: event.client_seq, event, bytes });
				maximum = Math.max(maximum, event.client_seq);
			} catch {
				// A partial final append is an expected crash window; preserve all
				// complete records before it and omit the malformed fragment.
			}
		}
		this.records.sort((a, b) => a.seq - b.seq);
		this.nextSeq = maximum + 1;
	}

	ready(): Promise<void> {
		return this.tail.then(() => undefined);
	}

	private serialize<T>(operation: () => Promise<T>): Promise<T> {
		const result = this.tail.then(operation, operation);
		this.tail = result.then(() => undefined, () => undefined);
		return result;
	}

	append(event: Record<string, unknown>): Promise<SpoolEvent | undefined> {
		return this.serialize(async () => {
			const seq = this.nextSeq++;
			const identified: SpoolEvent = {
				...event,
				producer_id: this.producerId,
				event_id: `${this.producerId}:${seq}`,
				client_seq: seq,
			};
			const line = `${JSON.stringify(identified)}\n`;
			const bytes = Buffer.byteLength(line);
			const totalBytes = this.records.reduce((sum, record) => sum + record.bytes, 0);

			// Persist sequence consumption even for a dropped event. Reusing an
			// identity after restart is more dangerous than a harmless gap.
			await this.atomicWrite(this.sequencePath, String(seq));
			if (this.records.length >= this.maxEvents || totalBytes + bytes > this.maxBytes) {
				this.droppedCount += 1;
				return undefined;
			}
			await this.fs.appendFile(this.path, line, "utf8");
			this.records.push({ seq, event: identified, bytes });
			return identified;
		});
	}

	peek(limit: number): Promise<SpoolRecord[]> {
		return this.serialize(async () => this.records.slice(0, Math.max(0, limit)));
	}

	/** Atomically remove one acknowledged prefix; never deletes later records. */
	acknowledgeThrough(seq: number): Promise<number> {
		return this.serialize(async () => {
			let count = 0;
			while (count < this.records.length && this.records[count].seq <= seq) count += 1;
			if (count === 0) return 0;
			const remaining = this.records.slice(count);
			await this.atomicWrite(this.path, remaining.map((record) => `${JSON.stringify(record.event)}\n`).join(""));
			this.records = remaining;
			return count;
		});
	}

	stats(): Promise<SpoolStats> {
		return this.serialize(async () => ({
			diskEvents: this.records.length,
			diskBytes: this.records.reduce((sum, record) => sum + record.bytes, 0),
			dropped: this.droppedCount,
		}));
	}

	private async atomicWrite(path: string, contents: string): Promise<void> {
		const temporary = `${path}.tmp`;
		await this.fs.writeFile(temporary, contents, "utf8");
		await this.fs.rename(temporary, path);
	}
}
