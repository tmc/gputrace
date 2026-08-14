// perfetto-ui-smoke.mjs drives a Chromium browser over the DevTools protocol
// and reports what a pinned Perfetto UI actually renders for a gputrace trace.
//
// It uses only Node built-ins. The browser must already be listening with
// --remote-debugging-port; the shell wrapper starts and stops it.
//
// Usage: node perfetto-ui-smoke.mjs DEBUG_PORT HOST_URL
//
// It prints one JSON object describing the rendered tracks and the selected
// slice, and exits non-zero when the trace does not become visible.

const [, , port, hostURL] = process.argv;
if (!port || !hostURL) {
  console.error("usage: perfetto-ui-smoke.mjs DEBUG_PORT HOST_URL");
  process.exit(2);
}

const deadline = Date.now() + 180_000;

async function browserWebSocket() {
  const response = await fetch(`http://127.0.0.1:${port}/json/version`);
  const {webSocketDebuggerUrl} = await response.json();
  return webSocketDebuggerUrl;
}

// session multiplexes DevTools commands over one connection. Perfetto's UI
// lives in a same-origin iframe, so one page target covers host and UI.
class session {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 1;
    this.pending = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      const waiter = this.pending.get(message.id);
      if (!waiter) return;
      this.pending.delete(message.id);
      if (message.error) waiter.reject(new Error(JSON.stringify(message.error)));
      else waiter.resolve(message.result);
    });
  }

  send(method, params = {}, sessionId) {
    const id = this.nextID++;
    const frame = {id, method, params};
    if (sessionId) frame.sessionId = sessionId;
    this.socket.send(JSON.stringify(frame));
    return new Promise((resolve, reject) => this.pending.set(id, {resolve, reject}));
  }
}

async function open(url) {
  const socket = new WebSocket(url);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, {once: true});
    socket.addEventListener("error", reject, {once: true});
  });
  return new session(socket);
}

// evaluate runs an expression in the page and returns its resolved value.
async function evaluate(cdp, sessionId, expression) {
  const result = await cdp.send(
    "Runtime.evaluate",
    {expression, awaitPromise: true, returnByValue: true},
    sessionId,
  );
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.exception?.description ?? "evaluation failed");
  }
  return result.result.value;
}

async function until(cdp, sessionId, expression, what) {
  for (;;) {
    const value = await evaluate(cdp, sessionId, expression);
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
}

const cdp = await open(await browserWebSocket());
const {targetId} = await cdp.send("Target.createTarget", {url: "about:blank"});
const {sessionId} = await cdp.send("Target.attachToTarget", {targetId, flatten: true});
await cdp.send("Page.enable", {}, sessionId);
await cdp.send("Runtime.enable", {}, sessionId);
await cdp.send("Page.navigate", {url: hostURL}, sessionId);

// The host page posts the trace only after the UI answers PONG. The UI is
// same-origin here, so the host document can read the UI's rendered DOM.
const uiReady = `(() => {
  const doc = document.querySelector("iframe")?.contentDocument;
  return !!doc && !!doc.querySelector("canvas");
})()`;
await until(cdp, sessionId, uiReady, "the embedded Perfetto UI to render");

// Reading the rendered track titles rather than the UI's internal state keeps
// this a check on what a user sees. The class names below are the ones the
// pinned build emits; a UI upgrade that renames them makes this gate fail
// loudly rather than pass vacuously.
const trackText = `(() => {
  const doc = document.querySelector("iframe").contentDocument;
  const names = [];
  for (const node of doc.querySelectorAll(".pf-track__title")) {
    const text = (node.textContent || "").trim();
    if (text) names.push(text);
  }
  return JSON.stringify(names);
})()`;
await until(cdp, sessionId, trackText, "rendered track names");

// Groups render collapsed, so the encoder and dispatch tracks are not in the
// DOM until something expands them. Clicking the collapse buttons is the
// gesture a user makes; repeat until no button reports a further expansion.
const expand = `(() => {
  const doc = document.querySelector("iframe").contentDocument;
  const before = doc.querySelectorAll(".pf-track__title").length;
  for (const node of doc.querySelectorAll(".pf-track__collapse-button")) node.click();
  return before;
})()`;
for (let round = 0; round < 8; round++) {
  const before = await evaluate(cdp, sessionId, expand);
  await new Promise((resolve) => setTimeout(resolve, 1500));
  const after = JSON.parse(await evaluate(cdp, sessionId, trackText)).length;
  if (after <= before) break;
}
const names = JSON.parse(await evaluate(cdp, sessionId, trackText));

const report = {
  hostURL,
  uiRevision: await evaluate(
    cdp,
    sessionId,
    `document.querySelector('meta[name=gputrace-perfetto-ui]')?.content ?? ""`,
  ),
  trackCount: names.length,
  trackNames: names,
};
console.log(JSON.stringify(report, null, 2));

await cdp.send("Target.closeTarget", {targetId});
cdp.socket.close();

if (report.trackCount === 0) {
  console.error("the pinned Perfetto UI rendered no named tracks");
  process.exit(1);
}
