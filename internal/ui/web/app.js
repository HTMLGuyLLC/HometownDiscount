// The Hometown Discount scorecard. It reads /api/report from htd on this
// computer every few seconds and draws it. Everything is built with DOM
// calls and textContent, so nothing in the data is ever run as markup.
"use strict";

const REFRESH_MS = 10000;
const SVG = "http://www.w3.org/2000/svg";
const STOPPED = "htd ui has stopped. Run `htd ui` again; it opens a fresh page.";
const EXPIRED = "This page's link has expired. Run `htd ui` again; it opens a fresh page.";

let report = null;
let lastSeen = ""; // the last data drawn, so an unchanged refresh doesn't redraw
let open = null; // the open cell: {model, kind, task, size}
let lastChartW = 0; // the width the over-time chart was last drawn at
const replaysOpen = new Set(); // models whose replayed jobs are unfolded

// The theme: [ui] theme in config.toml, remembered so the next visit paints
// in it straight away. "auto" follows the computer.
function applyTheme(theme) {
  const root = document.documentElement;
  if (theme === "light" || theme === "dark") root.dataset.theme = theme;
  else delete root.dataset.theme;
  try { localStorage.setItem("htd-theme", theme); } catch { /* private window */ }
}
try { applyTheme(localStorage.getItem("htd-theme") || "auto"); } catch { /* no storage */ }

// el builds an element: el("td", {class: "x"}, "text", child, ...).
function el(tag, attrs, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else e.setAttribute(k, v === true ? "" : v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === undefined || kid === null || kid === false) continue;
    e.append(kid instanceof Node ? kid : String(kid));
  }
  return e;
}
function svgEl(tag, attrs, ...kids) {
  const e = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
  for (const kid of kids) e.append(kid instanceof Node ? kid : String(kid));
  return e;
}

// Focus survives a redraw: controls that get rebuilt carry a stable
// data-key. focusKey reads the key of the focused control before a redraw;
// restoreFocus puts focus on the rebuilt control with the same key (or the
// first fallback key that exists).
function focusKey() {
  const a = document.activeElement;
  const k = a && a.closest ? a.closest("[data-key]") : null;
  return k ? k.dataset.key : "";
}
function restoreFocus(key, fallbacks) {
  const all = [...document.querySelectorAll("[data-key]")];
  for (const want of [key, ...(fallbacks || [])]) {
    if (!want) continue;
    const t = all.find((x) => x.dataset.key === want);
    if (t) { t.focus({ preventScroll: true }); return true; }
  }
  return false;
}
const reducedMotion = () => window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

// The red box at the top. role="alert" on it makes a screen reader say it.
function showError(msg) {
  const box = document.getElementById("error");
  if (box.textContent !== msg) box.textContent = msg;
  box.hidden = false;
}
function hideError() {
  document.getElementById("error").hidden = true;
}

class ApiError extends Error {}

// api fetches one of htd's JSON endpoints. It resolves {ok, status, body}
// for any reply htd sent. If htd can't be reached, or says the link has
// expired (401), it shows that in the red box and throws an ApiError.
async function api(path, init) {
  let res;
  try {
    res = await fetch(path, init);
  } catch {
    showError(STOPPED); // TypeError, "Failed to fetch", Safari's "Load failed"...
    throw new ApiError(STOPPED);
  }
  const body = await res.json().catch(() => ({}));
  if (res.status === 401) {
    showError(EXPIRED);
    throw new ApiError(EXPIRED);
  }
  return { ok: res.ok, status: res.status, body };
}

const n = (x) => Number(x || 0).toLocaleString();
const pct = (x) => Math.round(x * 100) + "%";
const plural = (k, one, many) => (k === 1 ? one : many);
const cap = (s) => s.charAt(0).toUpperCase() + s.slice(1);
function secs(s) {
  if (!s || s <= 0) return "-";
  if (s < 90) return Math.round(s) + "s";
  return (s / 60).toFixed(1) + "m";
}
function day(iso) {
  return iso ? new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" }) : "";
}
function dateOnly(s) {
  const [y, m, d] = s.split("-").map(Number);
  return new Date(y, m - 1, d).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
function when(iso) {
  return new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

const PILL = { local: "Goes local", slow: "Stays with Claude", learning: "Learning", no: "Not good enough" };

// cellLine is the one quiet line under a result.
function cellLine(c) {
  const right = `${c.passed} of ${c.graded} right`;
  switch (c.result) {
    case "local": return c.pace_words ? `${right} · ${c.pace_words}` : right;
    case "slow": return c.never_local ? `${right} · your setting` : c.pace_words ? `${right} · ${c.pace_words}` : right;
    case "learning": return c.next ? `${right} · decided after ${c.next} checks` : right;
  }
  return right;
}

async function refresh() {
  const live = document.getElementById("live");
  try {
    const { ok, status, body } = await api("api/report", { cache: "no-store" });
    if (!ok) {
      showError("htd couldn't read your results: " + (body.error || "error " + status));
      live.classList.add("stale");
      live.textContent = "Not updating";
      return;
    }
    report = body;
    hideError();
    applyTheme(report.theme);
    const seen = JSON.stringify({ ...body, generated: "" });
    if (seen !== lastSeen) {
      lastSeen = seen;
      render();
    }
    live.classList.remove("stale");
    live.textContent = `Live · updated ${new Date(report.generated).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`;
  } catch {
    live.classList.add("stale");
    live.textContent = "Not updating";
  } finally {
    settleTab();
  }
}

function render() {
  const key = focusKey();
  const r = report;
  renderConfigError(r.config_error);
  document.getElementById("empty").hidden = r.models.length > 0;
  renderTotals(r.totals);
  renderSavings(r.savings);
  document.getElementById("models-wrap").hidden = r.models.length === 0;
  document.getElementById("models").replaceChildren(...r.models.map(renderModel));
  renderOverTime(r);
  renderHandoffs(r.handoffs);
  if (key) restoreFocus(key);
}

// When config.toml can't be read, htd runs on defaults. Say so on the two
// tabs that show results, and point at Settings.
function renderConfigError(msg) {
  for (const box of document.querySelectorAll(".config-error")) {
    box.hidden = !msg;
    box.querySelector(".msg").textContent = msg || "";
  }
}

const stat = (num, label, sub, good) =>
  el("div", { class: good ? "stat good" : "stat" },
    el("div", { class: "n" }, num),
    el("div", { class: "l" }, label),
    [].concat(sub || []).filter(Boolean).map((x) => el("div", { class: "s" }, x)));
const money = (v) => (v >= 100 ? "$" + Math.round(v).toLocaleString() : "$" + v.toFixed(2));

function renderTotals(t) {
  document.getElementById("totals").replaceChildren(
    stat(n(t.go_local), plural(t.go_local, "task size goes local now", "task sizes go local now"), null, t.go_local > 0),
    stat(n(t.graded), "checks and replays graded", `${n(t.models)} ${plural(t.models, "model", "models")} tested`),
    stat(t.last_run ? day(t.last_run) : "-", "last check or replay", "Run htd bench or htd replay for more.")
  );
}

// Why Claude kept a task instead of handing it off, in plain words.
const WHY = [
  ["unproven", "not proven yet"],
  ["slow", "too slow"],
  ["unreachable", "model not reachable"],
  ["too_big", "too big for the model"],
  ["context", "didn't fit in the model's memory"],
  ["other", "other"],
];
function refusedLine(s) {
  if (!s.refused) return null;
  const why = s.refused_why || {};
  const parts = WHY.filter(([k]) => why[k]).map(([k, w]) => `${n(why[k])} ${w}`);
  return `${n(s.refused)} kept by Claude` + (parts.length ? ": " + parts.join(", ") : "");
}

const card = (title, big, lines, cls) =>
  el("div", { class: "panel estimate" }, el("h3", {}, title), el("div", { class: cls ? "big " + cls : "big" }, big), lines.map((l) => el("p", {}, l)));

// The Savings tab.
function renderSavings(s) {
  const any = s.answered + s.refused + s.failed > 0;
  document.getElementById("tracking-off").hidden = s.tracking;
  document.getElementById("savings-empty").hidden = any || !s.tracking;
  document.getElementById("savings-totals").hidden = !any;
  const at = s.worth_model ? `at ${s.worth_model} API prices` : "no price known for your Claude model";
  document.getElementById("savings-totals").replaceChildren(
    stat([n(s.answered), el("span", { class: "unit" }, " " + plural(s.answered, "task", "tasks"))],
      "Work Claude handed to your local models",
      [refusedLine(s), s.failed > 0 ? `${n(s.failed)} failed` : null], s.answered > 0),
    stat([el("span", { class: "unit" }, "about "), n(s.tokens_kept)], "tokens kept out of Claude", s.first ? `since ${day(s.first)}` : null),
    stat(s.worth ? [el("span", { class: "unit" }, "worth about "), money(s.worth)] : "-", at, "On a Pro or Max plan that's usage headroom, not dollars.")
  );

  // Nothing to estimate from until Claude has handed something off.
  document.getElementById("estimates-wrap").hidden = !any;
  const est = [];
  if (s.pace) {
    const p = s.pace;
    const newer = s.first && new Date(s.first).getTime() > Date.now() - p.days * 86400000;
    est.push(card("At your current pace", p.worth ? `~${money(p.worth)} a month` : `~${n(p.tasks)} tasks a month`, [
      `About ${n(p.tasks)} ${plural(p.tasks, "task", "tasks")} and ${n(p.tokens)} tokens a month kept out of Claude.`,
      newer ? `Based on your handoffs since ${day(s.first)}.` : `Based on your handoffs over the last ${p.days} days.`,
    ]));
  } else {
    est.push(card("At your current pace", "-", [s.tracking ? "No handoffs in the last 30 days to go by." : "Turn on handoff tracking to see this."]));
  }
  if (s.room_status === "reading") {
    est.push(card("Room to grow", "…", ["Reading your Claude Code transcripts to see how much of Claude's file reading your models could take. This can take a minute."], "ink"));
  } else if (s.room_status === "error") {
    est.push(card("Room to grow", "-", [s.room_error || "htd couldn't read your Claude Code transcripts."], "ink"));
  } else if (s.room) {
    const r = s.room;
    est.push(card("Room to grow", r.worth ? `up to ${money(r.worth)} a month` : `up to ${n(r.tasks)} reads a month`, [
      `Claude read about ${n(r.tasks)} ${plural(r.tasks, "file", "files")} a month (${r.sizes}), about ${n(r.tokens)} tokens. Your models go local for: ${r.skills}.`,
      `A ceiling, not a promise: it counts every such read in the last ${r.days} days of transcripts as if it were one of those tasks. Most won't be.`,
    ], "ink"));
  } else {
    est.push(card("Room to grow", "-", ["Once a model goes local for a skill (Models tab), htd estimates how much of Claude's file reading it could take."], "ink"));
  }
  document.getElementById("estimates").replaceChildren(...est);

  const rows = (list) => list.map((x) => el("tr", {}, el("td", {}, x.name || "Not recorded"), el("td", {}, n(x.tasks)), el("td", {}, n(x.tokens)), el("td", {}, x.worth ? money(x.worth) : "-")));
  document.getElementById("shares-wrap").hidden = s.answered === 0;
  document.getElementById("by-model").replaceChildren(...rows(s.by_model));
  document.getElementById("by-task").replaceChildren(...rows(s.by_task));
}

function renderModel(m) {
  const counts = [];
  for (const [key, many, one] of [["local", "go local", "goes local"], ["slow", "stay with Claude", "stays with Claude"], ["learning", "learning", "learning"], ["no", "not good enough", "not good enough"]]) {
    if (m.counts[key]) counts.push(el("span", { class: "pill " + key }, `${m.counts[key]} ${m.counts[key] === 1 ? one : many}`));
  }
  const box = el("article", { class: "model" },
    el("div", { class: "model-head" },
      el("div", {}, el("h3", {}, m.name), m.detail && el("div", { class: "sub" }, m.detail)),
      el("div", { class: "counts" }, counts)
    )
  );
  if (m.skills.length) {
    box.append(taskTable(m, "skills", m.skills, "Skill checks"));
    const d = detailFor(m, "skills", m.skills);
    if (d) box.append(d);
  }
  if (m.replays.length) {
    const more = el("details", { class: "more", open: replaysOpen.has(m.id) || (open && open.model === m.id && open.kind === "replays") },
      el("summary", { "data-key": "replays|" + m.id }, "Replayed jobs ", el("span", { class: "sub" }, `· ${m.replays.length} ${plural(m.replays.length, "kind", "kinds")}, your past Claude jobs redone locally`)),
      taskTable(m, "replays", m.replays, "Replayed jobs"),
      detailFor(m, "replays", m.replays)
    );
    more.addEventListener("toggle", () => { if (more.open) replaysOpen.add(m.id); else replaysOpen.delete(m.id); });
    box.append(more);
  }
  return box;
}

function detailFor(m, kind, list) {
  if (!open || open.model !== m.id || open.kind !== kind) return null;
  const task = list.find((t) => t.type + "|" + (t.vs || "") === open.task);
  const cell = task && task.cells.find((c) => c.size === open.size);
  if (!cell) { open = null; return null; }
  return detail(task, cell, kind);
}

const cellKey = (o) => ["cell", o.model, o.kind, o.task, o.size].join("|");

function taskTable(m, kind, tasks, title) {
  const sizes = [];
  for (const t of tasks) for (const c of t.cells) if (!sizes.some((s) => s.size === c.size)) sizes.push(c);
  const head = el("tr", {}, el("th", { scope: "col" }, title), sizes.map((s) => el("th", { scope: "col" }, cap(s.size), el("small", {}, s.size_words))));
  const rows = tasks.map((t) => {
    const key = t.type + "|" + (t.vs || "");
    return el("tr", {},
      el("th", { class: "task", scope: "row" }, el("b", {}, t.name), t.vs && el("span", {}, `vs ${t.vs}`)),
      sizes.map((s) => {
        const c = t.cells.find((x) => x.size === s.size);
        return el("td", {}, c && c.result !== "none" ? cellView(m, kind, key, t, c) : [el("span", { class: "dash", "aria-hidden": "true" }, "—"), el("span", { class: "sr-only" }, "not run")]);
      })
    );
  });
  return el("div", { class: "scroll" }, el("table", {}, el("thead", {}, head), el("tbody", {}, rows)));
}

function cellView(m, kind, key, t, c) {
  const me = { model: m.id, kind, task: key, size: c.size };
  const isOpen = open && cellKey(open) === cellKey(me);
  return el("button", {
    class: "cell", type: "button", "aria-expanded": isOpen ? "true" : "false", "data-key": cellKey(me),
    "aria-label": `${t.name}, ${c.size}: ${PILL[c.result]}. ${cellLine(c)}. Show details.`,
    onclick: () => {
      open = isOpen ? null : me;
      render(); // keeps focus on this cell
      const heading = open && document.querySelector('.detail [data-key="detail-heading"]');
      if (heading) {
        heading.focus({ preventScroll: true });
        heading.scrollIntoView({ block: "nearest", behavior: reducedMotion() ? "auto" : "smooth" });
      }
    },
  },
    el("span", { class: "pill " + c.result }, PILL[c.result] || c.result),
    el("span", { class: "line" }, cellLine(c))
  );
}

function standing(c, kind) {
  switch (c.result) {
    case "local": return "Goes local: accurate, and fast enough for your speed limit.";
    case "slow": return c.never_local
      ? "Accurate, but your settings keep it on Claude."
      : `Accurate, but too slow${c.pace_words ? ": " + c.pace_words : ""}${c.limit_words ? " (" + c.limit_words + ")" : ""}.`;
    case "no": return "Too many wrong answers to reach the bar. More checks won't change that soon.";
  }
  if (!c.next) return "Still learning.";
  const more = kind === "skills" ? "htd bench in another repo" : "htd replay";
  return `Still learning. Next check-in at ${c.next} checks, ${c.next - c.graded} to go. Run ${more} to get there.`;
}

function detail(task, c, kind) {
  const rate = c.graded ? c.passed / c.graded : 0;
  const fill = el("i", { class: c.result === "local" ? "good" : null });
  fill.style.width = pct(rate);
  const mark = el("u");
  mark.style.left = pct(c.bar);
  const limit = c.limit_words ? ` ${cap(c.limit_words)}.` : "";
  const facts = [
    ["Result", standing(c, kind)],
    ["Accuracy", el("span", { class: "acc" }, el("span", { class: "bar" }, fill, mark), `${c.passed} of ${c.graded} right (${pct(rate)}), ${pct(c.bar)} needed`)],
    ["Speed", kind === "skills"
      ? `${secs(c.secs)} each; Claude takes about ${secs(c.claude_secs)}.${limit}`
      : `${secs(c.secs)} each${c.pace_words ? "; " + c.pace_words + " on the same jobs" : ""}.${limit}`],
  ];
  if (c.skipped) facts.push(["Not graded", `${c.skipped} couldn't be graded and don't count`]);
  if (c.last) facts.push(["Last check", day(c.last)]);
  const close = el("button", {
    class: "close", type: "button", "data-key": "close",
    onclick: () => { const k = cellKey(open); open = null; render(); restoreFocus(k); },
  }, "Close");
  const looks = (report && report.looks) || [];
  return el("div", { class: "detail" },
    el("div", {},
      el("h4", { tabindex: "-1", "data-key": "detail-heading" }, `${task.name}${task.vs ? " vs " + task.vs : ""} · ${c.size_words}`),
      trendChart(c, looks),
      el("p", {}, `Solid line: share right so far. Dotted line: the share htd is sure of so far. It's proven when the dotted line reaches the ${pct(c.bar)} bar at a check-in${looks.length ? ` (after ${looks.join(", ")} checks)` : ""}.`)
    ),
    el("div", {}, el("div", { class: "head-row" }, el("h4", {}, "Where it stands"), close),
      el("dl", {}, facts.map(([k, v]) => [el("dt", {}, k), el("dd", {}, v)])))
  );
}

function trendChart(c, looks) {
  const pts = c.trend || [];
  if (!pts.length) return el("p", {}, "No graded checks yet.");
  // Draw at about the size it will be shown, so the text stays readable on a phone.
  const vw = window.innerWidth || 440;
  const avail = vw > 760 ? (Math.min(vw, 1040) - 130) * 0.555 : vw - 100;
  const W = Math.max(240, Math.min(440, Math.round(avail))), H = 170, L = 36, R = 10, T = 12, B = 28;
  const maxN = Math.max(pts[pts.length - 1].n, 2);
  const x = (k) => L + ((k - 1) / (maxN - 1)) * (W - L - R);
  const y = (v) => T + (1 - v) * (H - T - B);
  const s = svgEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": `Share right after each check, against the ${pct(c.bar)} bar` });
  for (const v of [0, 0.5, 1]) {
    s.append(svgEl("line", { class: "grid", x1: L, x2: W - R, y1: y(v), y2: y(v) }));
    s.append(svgEl("text", { x: L - 6, y: y(v) + 4, "text-anchor": "end" }, pct(v)));
  }
  for (const look of looks) {
    if (look > maxN) break;
    s.append(svgEl("line", { class: "look", x1: x(look), x2: x(look), y1: T, y2: H - B }));
  }
  s.append(svgEl("line", { class: "need", x1: L, x2: W - R, y1: y(c.bar), y2: y(c.bar) }));
  const line = (key, cls) => svgEl("polyline", { class: cls, points: pts.map((p) => `${x(p.n).toFixed(1)},${y(p[key]).toFixed(1)}`).join(" ") });
  s.append(line("lower", "lower"), line("rate", "rate"));
  const first = pts[0], last = pts[pts.length - 1];
  s.append(svgEl("text", { x: L, y: H - 6 }, `check 1 · ${dateOnly(first.at.slice(0, 10))}`));
  s.append(svgEl("text", { x: W - R, y: H - 6, "text-anchor": "end" }, `check ${last.n} · ${dateOnly(last.at.slice(0, 10))}`));
  return s;
}

function seriesColors() {
  const css = getComputedStyle(document.documentElement);
  return ["--series-1", "--series-2", "--series-3", "--series-4"].map((v) => css.getPropertyValue(v).trim());
}
// Each model's line has its own dash pattern, so colour is never the only cue.
const DASHES = ["none", "7 4", "2 3", "9 3 2 3"];

function renderOverTime(r) {
  const section = document.getElementById("over-time");
  section.hidden = r.days.length === 0 || r.models.length === 0;
  if (section.hidden) return;
  // Draw at the width it will be shown, so axis text stays 11px on a phone.
  const box = document.getElementById("chart");
  const W = Math.max(280, Math.round(box.clientWidth || lastChartW || 900)), H = 190, L = 28, R = 12, T = 12, B = 30;
  lastChartW = W;
  const days = r.days;
  const top = Math.max(1, ...days.flatMap((d) => Object.values(d.local)));
  const maxChecks = Math.max(1, ...days.map((d) => d.checks));
  const step = (W - L - R) / days.length;
  const x = (i) => L + i * step;
  const y = (v) => T + (1 - v / top) * (H - T - B);
  const s = svgEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "Task sizes that go local by day, per model" });
  const ticks = top <= 4 ? Array.from({ length: top + 1 }, (_, i) => i) : [0, Math.round(top / 2), top];
  for (const v of ticks) {
    s.append(svgEl("line", { class: "grid", x1: L, x2: W - R, y1: y(v), y2: y(v) }));
    s.append(svgEl("text", { x: L - 8, y: y(v) + 4, "text-anchor": "end" }, v));
  }
  days.forEach((d, i) => {
    if (!d.checks) return;
    const h = (d.checks / maxChecks) * 24;
    s.append(svgEl("rect", { class: "checks", x: x(i) + step * 0.25, width: step * 0.5, y: H - B - h, height: h, rx: 1 }));
  });
  const colors = seriesColors();
  const legend = [];
  r.models.forEach((m, k) => {
    const color = colors[k % colors.length];
    const dash = DASHES[k % DASHES.length];
    const pts = [];
    days.forEach((d, i) => {
      if (i > 0) pts.push(`${x(i).toFixed(1)},${y(days[i - 1].local[m.id] || 0).toFixed(1)}`);
      pts.push(`${x(i).toFixed(1)},${y(d.local[m.id] || 0).toFixed(1)}`);
    });
    const lastV = days[days.length - 1].local[m.id] || 0;
    pts.push(`${(W - R).toFixed(1)},${y(lastV).toFixed(1)}`);
    s.append(svgEl("polyline", { points: pts.join(" "), fill: "none", stroke: color, "stroke-width": 2.25, "stroke-linejoin": "round", "stroke-dasharray": dash }));
    const sw = svgEl("svg", { class: "swatch", viewBox: "0 0 28 8", width: 28, height: 8, "aria-hidden": "true" },
      svgEl("line", { x1: 1, x2: 27, y1: 4, y2: 4, stroke: color, "stroke-width": 2.25, "stroke-dasharray": dash }));
    legend.push(el("span", {}, sw, `${m.name}${m.thinking_off ? " (thinking off)" : ""}: ${lastV} now`));
  });
  // As many date labels as fit without touching.
  const fit = Math.max(2, Math.floor((W - L - R) / 64));
  const every = Math.ceil(days.length / fit);
  days.forEach((d, i) => {
    if (i % every === 0 && (days.length - 1 - i >= every || i === days.length - 1)) s.append(svgEl("text", { x: x(i) + 2, y: H - 8 }, dateOnly(d.date)));
    const words = r.models.map((m) => `${m.name}: ${d.local[m.id] || 0} go local`).join("; ");
    s.append(svgEl("rect", { x: x(i), y: T, width: step, height: H - T - B, fill: "transparent" },
      svgEl("title", {}, `${dateOnly(d.date)}: ${n(d.checks)} checks graded, ${n(d.handoffs)} handed off. ${words}`)));
  });
  document.getElementById("chart").replaceChildren(s);
  document.getElementById("chart-legend").replaceChildren(...legend);
}

function renderHandoffs(hs) {
  const section = document.getElementById("handoffs");
  section.hidden = hs.length === 0;
  const outcome = (h) => {
    if (h.outcome === "answered") return `Answered locally · ${n(h.tokens_kept)} tokens kept out of Claude`;
    if (h.outcome === "refused") return "Kept by Claude";
    return "Failed; Claude did it";
  };
  document.getElementById("handoff-rows").replaceChildren(...hs.map((h) => el("tr", {},
    el("td", {}, when(h.at)),
    el("td", {}, h.task),
    el("td", {}, h.lines ? `${h.size} · ${n(h.lines)} lines` : h.size),
    el("td", {}, h.model || "Not recorded"),
    el("td", {}, secs(h.secs)),
    el("td", {}, outcome(h))
  )));
}

// Tabs: Savings, Models and Settings. The address bar names the open one
// (#models), so a reload or a bookmark opens it again. With no tab named,
// nothing shows until the first report is in: a first-timer with no
// handoffs lands on Models, everyone else on Savings.
const TABS = ["savings", "models", "settings"];
let tabChosen = false;
function showTab(name) {
  if (!TABS.includes(name)) return;
  tabChosen = true;
  for (const t of TABS) {
    const tab = document.getElementById("tab-" + t);
    const on = t === name;
    tab.setAttribute("aria-selected", on ? "true" : "false");
    tab.tabIndex = on ? 0 : -1;
    document.getElementById("panel-" + t).hidden = !on;
  }
  if (name === "models" && report) renderOverTime(report); // it can only measure itself once shown
  if (name === "settings" && window.htdSettings) window.htdSettings.open();
}
function settleTab() {
  if (tabChosen) return;
  const s = report && report.savings;
  showTab(s && s.answered + s.refused + s.failed === 0 ? "models" : "savings");
}
function goTab(name) {
  if (location.hash.slice(1) === name) showTab(name);
  else location.hash = name;
}
document.querySelectorAll("[data-tab]").forEach((b) => b.addEventListener("click", () => goTab(b.dataset.tab)));
document.querySelectorAll("[data-goto]").forEach((b) => b.addEventListener("click", () => goTab(b.dataset.goto)));
document.querySelector(".tabs").addEventListener("keydown", (e) => {
  const i = TABS.indexOf(document.activeElement && document.activeElement.dataset.tab);
  if (i < 0) return;
  const next = e.key === "ArrowRight" ? (i + 1) % TABS.length
    : e.key === "ArrowLeft" ? (i + TABS.length - 1) % TABS.length
    : e.key === "Home" ? 0
    : e.key === "End" ? TABS.length - 1 : -1;
  if (next < 0) return;
  e.preventDefault();
  goTab(TABS[next]);
  document.getElementById("tab-" + TABS[next]).focus();
});
window.addEventListener("hashchange", () => showTab(location.hash.slice(1)));

let resizeTimer = 0;
window.addEventListener("resize", () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    const w = document.getElementById("chart").clientWidth;
    if (report && w && Math.abs(w - lastChartW) > 24) renderOverTime(report);
  }, 150);
});

refresh();
setInterval(() => { if (!document.hidden) refresh(); }, REFRESH_MS);
document.addEventListener("visibilitychange", () => { if (!document.hidden) refresh(); });
