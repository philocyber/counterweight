import { streamUrl } from "@/lib/api";

type StreamListener = (event: MessageEvent) => void;

// A ticket is single-use, so the browser's own reconnect (same URL) would present a spent one.
// TicketEventSource has the parts of the EventSource API the console uses, but closes a dropped
// connection itself and reopens it with a new ticket. The path may be a function, evaluated on
// every attempt, so a stream can resume from the last event it has seen (for example `since=`).
const RETRY_BASE_MS = 1000;
const RETRY_MAX_MS = 10_000;
// After this many consecutive HTTP-level refusals the stream stops retrying instead of looping.
const MAX_REFUSALS = 5;

export class TicketEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;

  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  readyState: number = TicketEventSource.CONNECTING;

  private readonly path: string | (() => string);
  private readonly listeners = new Map<string, Set<StreamListener>>();
  private source: EventSource | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private attempts = 0;
  private refusals = 0;
  private done = false;

  private isClosed(): boolean {
    return this.done;
  }

  constructor(path: string | (() => string)) {
    this.path = path;
    void this.connect();
  }

  addEventListener(type: string, listener: StreamListener): void {
    let set = this.listeners.get(type);
    if (!set) {
      set = new Set();
      this.listeners.set(type, set);
      if (this.source) this.bind(this.source, type);
    }
    set.add(listener);
  }

  close(): void {
    this.done = true;
    this.readyState = TicketEventSource.CLOSED;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.source?.close();
    this.source = null;
  }

  private bind(source: EventSource, type: string): void {
    source.addEventListener(type, (event) => {
      for (const listener of this.listeners.get(type) ?? []) listener(event as MessageEvent);
    });
  }

  private async connect(): Promise<void> {
    let url: string;
    try {
      url = await streamUrl(typeof this.path === "function" ? this.path() : this.path);
    } catch {
      if (this.done) return;
      this.dropped(new Event("error"), false);
      return;
    }
    if (this.done) return;
    const source = new EventSource(url);
    this.source = source;
    for (const type of this.listeners.keys()) this.bind(source, type);
    source.onopen = (event) => {
      this.attempts = 0;
      this.refusals = 0;
      this.readyState = TicketEventSource.OPEN;
      this.onopen?.(event);
    };
    source.onmessage = (event) => this.onmessage?.(event);
    source.onerror = (event) => {
      // The browser gives up (CLOSED) on a refused request and retries (CONNECTING) after a network drop.
      const refused = source.readyState === EventSource.CLOSED;
      source.close();
      if (this.source === source) this.source = null;
      this.dropped(event, refused);
    };
  }

  private dropped(event: Event, refused: boolean): void {
    this.readyState = TicketEventSource.CONNECTING;
    this.onerror?.(event);
    if (this.isClosed()) return;
    if (refused && ++this.refusals >= MAX_REFUSALS) {
      this.close();
      return;
    }
    const delay = Math.min(RETRY_BASE_MS * 2 ** this.attempts++, RETRY_MAX_MS);
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.connect();
    }, delay);
  }
}
