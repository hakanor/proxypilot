// Parses large JSON bodies off the main thread.
self.onmessage = (e: MessageEvent<string>) => {
  try {
    self.postMessage({ ok: true, value: JSON.parse(e.data) });
  } catch (err) {
    self.postMessage({ ok: false, error: String(err) });
  }
};
