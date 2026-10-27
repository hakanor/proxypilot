// Live event stream over WebSocket with automatic reconnect (exponential backoff).

export interface WireEvent {
  seq: number;
  type: string;
  time: string;
  data?: any;
}

export type StreamState =
  | { kind: 'connecting' }
  | { kind: 'open' }
  | { kind: 'waiting'; retryAt: number }
  | { kind: 'unauthorized' };

export interface StreamHandlers {
  onOpen(reconnect: boolean): void;
  onBatch(events: WireEvent[]): void;
  onDropped(count: number): void;
  onState(s: StreamState): void;
}

const events = ['flow.*', 'recording.changed', 'rule.changed', 'breakpoint.*', 'device.connected', 'chrome.state', 'proxy.state', 'suggestion'];

export class Stream {
  private ws?: WebSocket;
  private attempt = 0;
  private everOpened = false;
  private timer?: ReturnType<typeof setTimeout>;
  private stopped = false;

  constructor(private h: StreamHandlers) {}

  start() {
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    clearTimeout(this.timer);
    this.ws?.close();
  }

  /** Skip the backoff wait. */
  retryNow() {
    clearTimeout(this.timer);
    this.connect();
  }

  private connect() {
    if (this.stopped) return;
    this.h.onState({ kind: 'connecting' });
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(`${proto}//${location.host}/api/v1/stream`);
    this.ws = ws;
    let opened = false;
    ws.onopen = () => {
      opened = true;
      ws.send(JSON.stringify({ type: 'subscribe', events }));
      const reconnect = this.everOpened;
      this.everOpened = true;
      this.attempt = 0;
      this.h.onState({ kind: 'open' });
      this.h.onOpen(reconnect);
    };
    ws.onmessage = (m) => {
      let msg: any;
      try {
        msg = JSON.parse(m.data);
      } catch {
        return;
      }
      if (msg.type === 'batch') this.h.onBatch(msg.events ?? []);
      else if (msg.type === 'dropped') this.h.onDropped(msg.count ?? 0);
    };
    ws.onclose = async () => {
      if (this.ws !== ws || this.stopped) return;
      // A handshake that never opened may be an auth failure: ask the REST API.
      if (!opened) {
        try {
          const r = await fetch('/api/v1/status', { credentials: 'same-origin' });
          if (r.status === 401) {
            this.h.onState({ kind: 'unauthorized' });
            return;
          }
        } catch {
          /* daemon down */
        }
      }
      const delay = Math.min(10_000, 1000 * 2 ** this.attempt++);
      this.h.onState({ kind: 'waiting', retryAt: Date.now() + delay });
      this.timer = setTimeout(() => this.connect(), delay);
    };
  }
}
