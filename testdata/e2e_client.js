// Browser-engine smoke test driven by client_e2e_test.go: the real
// engine.wasm, a real WebSocket session and real /content downloads, doing
// what static/game.html does minus the canvas. Usage: node e2e_client.js <dir with engine.wasm + wasm_exec.js> <server base URL>
const fs = require("fs"), path = require("path");
const [,, dir, base] = process.argv;
require(path.join(dir, "wasm_exec.js"));
const go = new Go();
const fail = m => { console.error("E2E FAIL:", m); process.exit(1); };
globalThis.onRaycastReady = () => {
  const ws = new WebSocket(base.replace("http", "ws") + "/ws?engine=client");
  let cur = null, epoch = 0, poses = 0, msgs = 0, fetched = 0, fetchErr = 0, start = null;
  const keys = {};
  const inflight = new Set();
  const pump = () => {
    for (const p of raycast.wanted()) {
      if (inflight.has(p)) continue; inflight.add(p);
      fetch(`${base}/content/${p.split("/").map(encodeURIComponent).join("/")}?v=${raycast.version()}`)
        .then(r => { if (!r.ok) throw new Error(r.status); return r.arrayBuffer(); })
        .then(b => { raycast.addFile(p, new Uint8Array(b)); fetched++; })
        .catch(e => { fetchErr++; console.error("fetch", p, e.message); })
        .finally(() => inflight.delete(p));
    }
  };
  ws.onmessage = ev => {
    msgs++;
    const m = JSON.parse(ev.data);
    const err = raycast.apply(ev.data); if (err) fail(err);
    if (m.pose) { poses++; cur = { x: m.pose.x, y: m.pose.y, a: m.pose.a }; epoch = m.pose.e; if (!start) start = { ...cur }; }
    if (m.ev) for (const e of m.ev) console.log("event:", e.type);
    pump();
  };
  const out = new Uint8ClampedArray(320 * 240 * 4);
  let tick = 0;
  const sim = setInterval(() => {
    if (!cur) return;
    tick++;
    keys.ArrowUp = tick > 5 && tick < 25;     // walk forward 2 s
    keys.ArrowRight = tick > 10 && tick < 14; // turn a little
    const [x, y, a] = raycast.step(cur.x, cur.y, cur.a, keys);
    cur = { x, y, a };
    raycast.render(x, y, a, false, new Float64Array(0), out);
    pump();
  }, 100);
  const mover = setInterval(() => { if (cur) ws.send(JSON.stringify({ t: "move", x: cur.x, y: cur.y, a: cur.a, m: false, e: epoch })); }, 50);
  setTimeout(() => {
    clearInterval(sim); clearInterval(mover);
    let lit = 0; for (let i = 0; i < out.length; i += 4) if (out[i] | out[i + 1] | out[i + 2]) lit++;
    const moved = Math.hypot(cur.x - start.x, cur.y - start.y);
    console.log(JSON.stringify({ msgs, poses, fetched, fetchErr, moved: +moved.toFixed(1), lit }));
    if (msgs < 25) fail("too few server ticks");
    if (poses !== 1) fail(`server overrode the predicted pose ${poses - 1} time(s)`);
    if (fetchErr || !fetched) fail("texture downloads failed");
    if (moved < 64) fail("did not move");
    if (lit < 20000) fail("frame mostly black");
    console.log("E2E OK");
    ws.close(); process.exit(0);
  }, 3500);
};
WebAssembly.instantiate(fs.readFileSync(path.join(dir, "engine.wasm")), go.importObject).then(r => go.run(r.instance));
