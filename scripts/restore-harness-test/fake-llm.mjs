#!/usr/bin/env node
// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar
//
// One local model for every harness in the restore rig, in each one's own
// wire format: Anthropic Messages (claude), OpenAI Chat Completions (qwen,
// opencode), OpenAI Responses (codex) and Gemini (gemini). It answers every
// turn with "noted: <what the user said>" — the harness keeps the
// conversation; the model only has to answer in the right shape.
import * as http from "node:http";
import * as fs from "node:fs";

const PORT = Number(process.env.FAKE_LLM_PORT ?? 8099);
// Kept where a reboot does not clear it (macOS empties /tmp at boot).
const LOG = process.env.FAKE_LLM_LOG ?? "/tmp/fake-llm.log";
const log = (...a) => fs.appendFileSync(LOG, a.join(" ") + "\n");

// The last thing a user said, in any of the request shapes.
function lastUserText(body) {
  const texts = [];
  const walk = (content) => {
    if (typeof content === "string") texts.push(content);
    else if (Array.isArray(content)) for (const p of content) {
      if (typeof p === "string") texts.push(p);
      else if (p && typeof p.text === "string" && p.type !== "tool_result") texts.push(p.text);
      else if (p && p.content) walk(p.content);
    }
  };
  const msgs = body.messages || body.contents || (Array.isArray(body.input) ? body.input : []);
  for (let i = msgs.length - 1; i >= 0; i--) {
    const m = msgs[i];
    if (!m || (m.role && m.role !== "user")) continue;
    texts.length = 0;
    walk(m.content ?? m.parts);
    const t = texts.map(s => s.trim()).filter(s => s && !s.startsWith("<")).pop();
    if (t) return t;
  }
  if (typeof body.input === "string") return body.input;
  return "";
}

const reply = (body) => {
  const said = lastUserText(body).replace(/\s+/g, " ").slice(0, 80);
  return said ? `noted: ${said}` : "noted.";
};

// A long turn on demand: a prompt that says "take your time" is answered
// only after SLOW_MS, the stream held open with keepalive comments (which
// every SSE reader ignores) — so a check can see what a harness does with
// keys typed while it is working, or with an interrupt. The log says when
// the hold began, ended, or was cut short by the client hanging up.
const SLOW_MS = Number(process.env.FAKE_LLM_SLOW_MS ?? 40000);
const slowTurn = (body) => /take your time/i.test(lastUserText(body));
// keepalive is what the stream is fed every few seconds while it is held:
// an SSE comment where the reader ignores those (OpenAI's two shapes), a
// ping event for Anthropic's, and an empty content chunk for Gemini's —
// whose reader gives up on a stream with no chunk in it and sends the whole
// turn again.
async function hold(req, res, body, keepalive) {
  if (!slowTurn(body)) return true;
  log("slow: holding", SLOW_MS, "ms");
  // The socket, not the request: a request's own "close" fires once its
  // body has been read, long before anyone hangs up.
  const sock = req.socket;
  const end = Date.now() + SLOW_MS;
  while (Date.now() < end) {
    await new Promise(r => setTimeout(r, Math.min(5000, end - Date.now())));
    if (sock.destroyed) { log("slow: client hung up after", SLOW_MS - (end - Date.now()), "ms"); return false; }
    if (keepalive) keepalive();
  }
  log("slow: released");
  return true;
}
const comment = (res) => () => res.write(": keepalive\n\n");

function sseHead(res) {
  res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache", connection: "keep-alive" });
}
const ev = (res, event, data) => res.write((event ? `event: ${event}\n` : "") + `data: ${JSON.stringify(data)}\n\n`);

async function anthropic(req, res, body) {
  const text = reply(body);
  const id = "msg_" + Date.now();
  const usage = { input_tokens: 10, output_tokens: 5, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 };
  if (!body.stream) {
    if (!await hold(req, res, body, null)) return;
    res.writeHead(200, { "content-type": "application/json" });
    return res.end(JSON.stringify({ id, type: "message", role: "assistant", model: body.model, content: [{ type: "text", text }], stop_reason: "end_turn", stop_sequence: null, usage }));
  }
  sseHead(res);
  ev(res, "message_start", { type: "message_start", message: { id, type: "message", role: "assistant", model: body.model, content: [], stop_reason: null, stop_sequence: null, usage } });
  if (!await hold(req, res, body, () => ev(res, "ping", { type: "ping" }))) return;
  ev(res, "content_block_start", { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } });
  ev(res, "content_block_delta", { type: "content_block_delta", index: 0, delta: { type: "text_delta", text } });
  ev(res, "content_block_stop", { type: "content_block_stop", index: 0 });
  ev(res, "message_delta", { type: "message_delta", delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 5 } });
  ev(res, "message_stop", { type: "message_stop" });
  res.end();
}

async function chat(req, res, body) {
  const text = reply(body);
  const id = "chatcmpl-" + Date.now(), created = Math.floor(Date.now() / 1000), model = body.model || "fake";
  const usage = { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 };
  if (!body.stream) {
    if (!await hold(req, res, body, null)) return;
    res.writeHead(200, { "content-type": "application/json" });
    return res.end(JSON.stringify({ id, object: "chat.completion", created, model, choices: [{ index: 0, message: { role: "assistant", content: text }, finish_reason: "stop" }], usage }));
  }
  sseHead(res);
  const chunk = (delta, finish) => ev(res, null, { id, object: "chat.completion.chunk", created, model, choices: [{ index: 0, delta, finish_reason: finish }] });
  chunk({ role: "assistant", content: "" }, null);
  if (!await hold(req, res, body, comment(res))) return;
  chunk({ content: text }, null);
  chunk({}, "stop");
  ev(res, null, { id, object: "chat.completion.chunk", created, model, choices: [], usage });
  res.write("data: [DONE]\n\n");
  res.end();
}

async function responses(req, res, body) {
  const text = reply(body);
  const id = "resp_" + Date.now(), mid = "msg_" + Date.now();
  const item = { id: mid, type: "message", role: "assistant", status: "completed", content: [{ type: "output_text", text, annotations: [] }] };
  const usage = { input_tokens: 10, input_tokens_details: { cached_tokens: 0 }, output_tokens: 5, output_tokens_details: { reasoning_tokens: 0 }, total_tokens: 15 };
  const resp = (status, output) => ({ id, object: "response", created_at: Math.floor(Date.now() / 1000), status, model: body.model, output, usage });
  if (!body.stream) {
    if (!await hold(req, res, body, null)) return;
    res.writeHead(200, { "content-type": "application/json" });
    return res.end(JSON.stringify(resp("completed", [item])));
  }
  sseHead(res);
  let seq = 0;
  const e = (type, extra) => ev(res, type, { type, sequence_number: seq++, ...extra });
  e("response.created", { response: resp("in_progress", []) });
  if (!await hold(req, res, body, comment(res))) return;
  e("response.output_item.added", { output_index: 0, item: { ...item, status: "in_progress", content: [] } });
  e("response.content_part.added", { item_id: mid, output_index: 0, content_index: 0, part: { type: "output_text", text: "", annotations: [] } });
  e("response.output_text.delta", { item_id: mid, output_index: 0, content_index: 0, delta: text });
  e("response.output_text.done", { item_id: mid, output_index: 0, content_index: 0, text });
  e("response.content_part.done", { item_id: mid, output_index: 0, content_index: 0, part: item.content[0] });
  e("response.output_item.done", { output_index: 0, item });
  e("response.completed", { response: resp("completed", [item]) });
  res.end();
}

async function gemini(req, res, body, url) {
  if (url.includes(":countTokens")) {
    res.writeHead(200, { "content-type": "application/json" });
    return res.end(JSON.stringify({ totalTokens: 10 }));
  }
  const text = reply(body);
  const out = { candidates: [{ content: { parts: [{ text }], role: "model" }, finishReason: "STOP", index: 0 }],
    usageMetadata: { promptTokenCount: 10, candidatesTokenCount: 5, totalTokenCount: 15 }, modelVersion: "fake" };
  if (url.includes(":streamGenerateContent")) {
    sseHead(res);
    if (!await hold(req, res, body, () => ev(res, null, { candidates: [{ content: { parts: [{ text: "" }], role: "model" }, index: 0 }] }))) return;
    ev(res, null, out);
    return res.end();
  }
  if (!await hold(req, res, body, null)) return;
  res.writeHead(200, { "content-type": "application/json" });
  res.end(JSON.stringify(out));
}

http.createServer((req, res) => {
  let raw = "";
  req.on("data", d => raw += d);
  req.on("end", () => {
    let body = {};
    try { body = raw ? JSON.parse(raw) : {}; } catch (_) {}
    const url = req.url || "";
    // Enough of the prompt to see what was typed in, past any prefix a
    // harness or reminal puts before it.
    log(new Date().toISOString(), req.method, url, JSON.stringify(lastUserText(body)).slice(0, 240));
    try {
      const done = (p) => p && p.catch && p.catch(e => log("error", e.stack));
      if (url.includes("/messages/count_tokens")) { res.writeHead(200, { "content-type": "application/json" }); return res.end('{"input_tokens":10}'); }
      if (url.includes("/messages")) return done(anthropic(req, res, body));
      if (url.includes("/chat/completions")) return done(chat(req, res, body));
      if (url.includes("/responses")) return done(responses(req, res, body));
      if (/generatecontent|:counttokens/i.test(url)) return done(gemini(req, res, body, url));
      if (url.includes("/models")) { res.writeHead(200, { "content-type": "application/json" }); return res.end(JSON.stringify({ object: "list", data: [{ id: "fake-model", object: "model", owned_by: "fake" }], models: [{ name: "models/fake-model" }] })); }
    } catch (e) { log("error", e.stack); }
    res.writeHead(404, { "content-type": "application/json" });
    res.end('{"error":{"message":"fake-llm: not handled"}}');
  });
}).listen(PORT, "127.0.0.1", () => log("fake-llm listening on", PORT));
