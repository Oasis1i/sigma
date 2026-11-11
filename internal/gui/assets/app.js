// The page is a client of the same engine the TUI uses. It loads history
// from /api/state, then applies live events from /api/events. Commands are
// POSTs. The launch token lives in the query string because EventSource
// cannot set an Authorization header.

const token = new URLSearchParams(location.search).get("token") || "";
const transcript = document.querySelector("#transcript");
const input = document.querySelector("#input");
const statusEl = document.querySelector("#status");
const stopBtn = document.querySelector("#stop");
const approval = document.querySelector("#approval");
const sessionsEl = document.querySelector("#sessions");

let mode = "ask";
let sessionID = "";
let running = false;
let pendingID = "";
let stick = true;

if (!token) {
  addNotice("This page was opened without a launch token. Start it with `sigma gui` and use the printed URL.");
}

transcript.addEventListener("scroll", () => {
  stick = transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight < 40;
});

document.querySelector("#composer").addEventListener("submit", (ev) => {
  ev.preventDefault();
  send();
});

input.addEventListener("keydown", (ev) => {
  if (ev.key === "Enter" && !ev.shiftKey) {
    ev.preventDefault();
    send();
  }
});

stopBtn.addEventListener("click", () => api("/api/cancel", { method: "POST" }));
document.querySelector("#allow").addEventListener("click", () => decide(true, false));
document.querySelector("#always").addEventListener("click", () => decide(true, true));
document.querySelector("#deny").addEventListener("click", () => decide(false, false));
document.querySelector("#new-session").addEventListener("click", async () => {
  const res = await api("/api/clear", { method: "POST" });
  if (res.ok) await refresh();
});

document.querySelectorAll(".mode button").forEach((btn) => {
  btn.addEventListener("click", async () => {
    const res = await api("/api/mode", {
      method: "POST",
      body: JSON.stringify({ mode: btn.dataset.mode }),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      addError(body.error || "could not change mode");
      return;
    }
    const body = await res.json();
    setMode(body.mode);
    addNotice("approval mode: " + body.mode);
  });
});

function api(path, opts = {}) {
  const headers = Object.assign({ Authorization: "Bearer " + token }, opts.headers || {});
  if (opts.body && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
  return fetch(path, Object.assign({}, opts, { headers }));
}

async function refresh() {
  const [stateRes, sessRes] = await Promise.all([
    api("/api/state"),
    api("/api/sessions"),
  ]);
  if (!stateRes.ok) {
    addError("could not load the session (" + stateRes.status + ")");
    return;
  }
  const state = await stateRes.json();
  sessionID = state.session_id;
  document.querySelector("#version").textContent = state.version ? "v" + state.version : "";
  const ws = document.querySelector("#workspace");
  ws.textContent = state.workspace || "";
  ws.title = state.workspace || "";
  setMode(state.mode);
  renderMessages(state.messages || []);
  setStatus(state);
  if (sessRes.ok) renderSessions(await sessRes.json());
}

function setMode(next) {
  mode = next || mode;
  document.querySelectorAll(".mode button").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.mode === mode);
  });
}

function setStatus(state) {
  const tokens = (state.prompt_tokens || 0) + (state.completion_tokens || 0);
  const tok = tokens > 0 ? " · " + tokens + " tok" : "";
  statusEl.textContent = (state.model || "") + " · " + (state.session_id || "") + tok;
}

function setRunning(on) {
  running = on;
  stopBtn.disabled = !on;
}

function renderMessages(messages) {
  transcript.replaceChildren();
  const tools = new Map();
  for (const msg of messages) {
    if (msg.role === "system") continue;
    if (msg.role === "user") {
      if (isModeNote(msg.content)) addNotice(msg.content.trim());
      else addUser(msg.content || "");
    }
    if (msg.role === "assistant") {
      if ((msg.content || "").trim()) addAssistant(msg.content, false);
      for (const call of msg.tool_calls || []) {
        const card = addTool(historyTool(call));
        tools.set(call.id, card);
      }
    }
    if (msg.role === "tool") {
      const card = tools.get(msg.tool_call_id);
      if (card) finishTool(card, msg.content || "", (msg.content || "").startsWith("Error:"));
    }
  }
  scrollDown();
}

function renderSessions(list) {
  sessionsEl.replaceChildren();
  for (const item of list) {
    const li = document.createElement("li");
    if (item.id === sessionID) li.className = "current";
    const id = document.createElement("div");
    id.className = "id";
    id.textContent = item.id;
    const preview = document.createElement("div");
    preview.className = "preview";
    preview.textContent = item.preview || item.model || "";
    li.append(id, preview);
    li.addEventListener("click", async () => {
      const res = await api("/api/resume", {
        method: "POST",
        body: JSON.stringify({ id: item.id }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        addError(body.error || "could not resume");
        return;
      }
      await refresh();
    });
    sessionsEl.append(li);
  }
}

async function send() {
  const text = input.value.trim();
  if (!text || !token) return;
  input.value = "";
  if (text.startsWith("/") && !text.includes("\n")) {
    const res = await api("/api/message", { method: "POST", body: JSON.stringify({ text }) });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      addError(body.error || "command failed");
      return;
    }
    if (body.notice) addNotice(body.notice);
    if (body.mode) setMode(body.mode);
    if (body.rebuild) await refresh();
    if (body.quit) addNotice("Close this tab to leave. The sigma gui process is still running in the terminal.");
    return;
  }
  addUser(text);
  setRunning(true);
  const res = await api("/api/message", { method: "POST", body: JSON.stringify({ text }) });
  if (res.status !== 202) {
    const body = await res.json().catch(() => ({}));
    addError(body.error || "could not send");
    setRunning(false);
  }
}

function decide(allow, always) {
  if (!pendingID) return;
  const id = pendingID;
  hideApproval();
  api("/api/approval", {
    method: "POST",
    body: JSON.stringify({ id, allow, always }),
  });
}

function hideApproval() {
  pendingID = "";
  approval.hidden = true;
}

function showApproval(ev) {
  pendingID = ev.approval_id;
  document.querySelector("#approval-title").textContent = "Allow " + (ev.tool || "tool") + "?";
  document.querySelector("#approval-arg").textContent = ev.args || "";
  approval.hidden = false;
}

function connect() {
  if (!token) return;
  const src = new EventSource("/api/events?token=" + encodeURIComponent(token));
  src.onmessage = (msg) => {
    let ev;
    try { ev = JSON.parse(msg.data); } catch (err) { return; }
    apply(ev);
  };
  src.onerror = () => {
    // EventSource reconnects on its own. A 401 stays failed; the missing
    // token case is already explained above.
  };
}

function apply(ev) {
  switch (ev.kind) {
    case "text_delta":
      appendDelta(ev.text || "");
      setRunning(true);
      break;
    case "tool_start":
      finishStream();
      addTool(ev);
      break;
    case "tool_end":
      finishTool(findTool(ev.call_id), ev.output || "", !!ev.failed, ev.diff);
      break;
    case "approval_request":
      showApproval(ev);
      break;
    case "step":
      setRunning(true);
      statusEl.textContent = "step " + ev.step + "/" + ev.max_steps + " · " + mode;
      break;
    case "usage":
      break;
    case "notice":
      addNotice(ev.text || "");
      break;
    case "error":
      addError(ev.text || "error");
      break;
    case "done":
      finishStream();
      setRunning(false);
      hideApproval();
      if (ev.canceled) addNotice("interrupted");
      refresh();
      break;
    default:
      break;
  }
  scrollDown();
}

function isModeNote(text) {
  return /^Approval mode is now (plan|ask|auto)\.$/.test((text || "").trim());
}

function addUser(text) {
  const el = bubble("user", "you");
  el.append(document.createTextNode(text));
  return el;
}

function addAssistant(text, streaming) {
  const el = bubble("assistant", "sigma");
  el.dataset.streaming = streaming ? "1" : "";
  el.append(renderMarkdown(text));
  return el;
}

function appendDelta(text) {
  let el = transcript.lastElementChild;
  if (!el || !el.classList.contains("assistant") || el.dataset.streaming !== "1") {
    el = addAssistant("", true);
  }
  el.dataset.raw = (el.dataset.raw || "") + text;
  const rendered = el.querySelector(".md");
  if (rendered) rendered.remove();
  el.append(renderMarkdown(el.dataset.raw));
}

function finishStream() {
  const el = transcript.lastElementChild;
  if (el && el.dataset.streaming === "1") el.dataset.streaming = "";
}

function addNotice(text) {
  const el = document.createElement("div");
  el.className = "msg notice";
  el.textContent = text;
  transcript.append(el);
  scrollDown();
}

function addError(text) {
  const el = document.createElement("div");
  el.className = "msg error";
  el.textContent = text;
  transcript.append(el);
  scrollDown();
}

function bubble(kind, who) {
  const el = document.createElement("div");
  el.className = "msg " + kind;
  const label = document.createElement("div");
  label.className = "who";
  label.textContent = who;
  el.append(label);
  transcript.append(el);
  return el;
}

function addTool(ev) {
  const details = document.createElement("details");
  details.className = "tool";
  details.open = true;
  details.dataset.callId = ev.call_id || ev.id || "";
  const summary = document.createElement("summary");
  summary.textContent = (ev.tool || ev.name || "tool") + (ev.preview ? "  " + ev.preview : "");
  const body = document.createElement("div");
  body.className = "body";
  if (ev.diff) body.append(renderDiff(ev.diff));
  details.append(summary, body);
  transcript.append(details);
  return details;
}

function findTool(id) {
  return transcript.querySelector('.tool[data-call-id="' + cssEscape(id) + '"]');
}

function finishTool(card, output, failed, diff) {
  if (!card) return;
  if (failed) card.classList.add("failed");
  const body = card.querySelector(".body");
  if (diff && !body.querySelector(".diff")) body.prepend(renderDiff(diff));
  if (output) {
    const pre = document.createElement("pre");
    pre.textContent = output;
    body.append(pre);
  }
}

function renderDiff(diff) {
  const wrap = document.createElement("div");
  wrap.className = "diff";
  for (const line of diff.split("\n")) {
    const div = document.createElement("div");
    div.textContent = line;
    if (line.startsWith("+")) div.className = "add";
    else if (line.startsWith("-")) div.className = "del";
    else if (line.startsWith("@@") || line.startsWith("---") || line.startsWith("+++")) div.className = "meta";
    wrap.append(div);
  }
  return wrap;
}

function scrollDown() {
  if (stick) transcript.scrollTop = transcript.scrollHeight;
}

function cssEscape(value) {
  if (window.CSS && CSS.escape) return CSS.escape(value || "");
  return String(value || "").replace(/"/g, "");
}

// renderMarkdown escapes first, then recognizes a small subset: fenced code,
// headings, lists, inline code, bold, and links. It is not CommonMark. The
// point is to show model replies without pulling a markdown library into the
// binary. HTML in the model output stays text.
function renderMarkdown(src) {
  const root = document.createElement("div");
  root.className = "md";
  const fences = [];
  const text = String(src || "").replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) => {
    fences.push({ lang, code });
    return "\u0000F" + (fences.length - 1) + "\u0000";
  });
  for (const block of text.split(/\n{2,}/)) {
    const trimmed = block.trim();
    if (!trimmed) continue;
    const fence = trimmed.match(/^\u0000F(\d+)\u0000$/);
    if (fence) {
      root.append(codeBlock(fences[Number(fence[1])]));
      continue;
    }
    if (/^#{1,3} /.test(trimmed)) {
      const level = trimmed.match(/^#+/)[0].length;
      const h = document.createElement("h" + level);
      h.innerHTML = inline(escapeHTML(trimmed.replace(/^#{1,3} /, "")));
      root.append(h);
      continue;
    }
    if (/^[-*] /.test(trimmed) || /^\d+\. /.test(trimmed)) {
      const ordered = /^\d+\. /.test(trimmed);
      const list = document.createElement(ordered ? "ol" : "ul");
      for (const line of trimmed.split("\n")) {
        const item = document.createElement("li");
        item.innerHTML = inline(escapeHTML(line.replace(/^([-*]|\d+\.) /, "")));
        list.append(item);
      }
      root.append(list);
      continue;
    }
    const p = document.createElement("p");
    const withBreaks = escapeHTML(trimmed).replace(/\n/g, "<br>");
    p.innerHTML = inline(withBreaks);
    root.append(p);
  }
  return root;
}

function codeBlock(fence) {
  const pre = document.createElement("pre");
  const code = document.createElement("code");
  code.innerHTML = highlight(fence.code || "", fence.lang || "");
  pre.append(code);
  return pre;
}

function inline(escaped) {
  return escaped
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" rel="noreferrer">$1</a>');
}

function historyTool(call) {
  let args = {};
  try { args = JSON.parse(call.arguments || "{}"); } catch (err) { args = {}; }
  let preview = "";
  if (call.name === "bash") preview = args.command || "";
  else if (typeof args.path === "string") preview = args.path;
  let diff = "";
  if (call.name === "edit_file") {
    const path = args.path || "file";
    diff = "--- a/" + path + "\n+++ b/" + path + "\n";
    for (const line of String(args.old_string || "").split("\n")) diff += "-" + line + "\n";
    for (const line of String(args.new_string || "").split("\n")) diff += "+" + line + "\n";
  }
  return { id: call.id, name: call.name, preview, diff };
}

function highlight(code) {
  const re = /(\/\/.*$|#.*$|\/\*[\s\S]*?\*\/|"[^"\n]*"|'[^'\n]*'|\b\d+\b|\b(?:func|return|if|else|for|package|import|const|let|var|type|struct|interface|def|class|fn|pub|async|await)\b)/gm;
  let out = "";
  let last = 0;
  for (const match of code.matchAll(re)) {
    out += escapeHTML(code.slice(last, match.index));
    const tok = match[0];
    let cls = "tok-kw";
    if (tok.startsWith("//") || tok.startsWith("#") || tok.startsWith("/*")) cls = "tok-com";
    else if (tok.startsWith('"') || tok.startsWith("'")) cls = "tok-str";
    else if (/^\d+$/.test(tok)) cls = "tok-num";
    out += '<span class="' + cls + '">' + escapeHTML(tok) + "</span>";
    last = match.index + tok.length;
  }
  out += escapeHTML(code.slice(last));
  return out;
}

function escapeHTML(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

refresh().then(connect);
