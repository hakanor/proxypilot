const WORKER_THRESHOLD = 256 * 1024;

/** Parses JSON; bodies above 256 KB go to a Web Worker so the tab never freezes. */
export function parseJSON(text: string): Promise<unknown> {
  if (text.length < WORKER_THRESHOLD) {
    return new Promise((res, rej) => {
      try {
        res(JSON.parse(text));
      } catch (e) {
        rej(e);
      }
    });
  }
  return new Promise((res, rej) => {
    const w = new Worker(new URL('./json.worker.ts', import.meta.url), { type: 'module' });
    w.onmessage = (e) => {
      w.terminate();
      e.data.ok ? res(e.data.value) : rej(new Error(e.data.error));
    };
    w.onerror = (e) => {
      w.terminate();
      rej(e);
    };
    w.postMessage(text);
  });
}
