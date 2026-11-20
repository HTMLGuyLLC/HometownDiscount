// The Hometown Discount scorecard. It reads /api/report from htd on this
// computer every few seconds and draws it. Everything is built with DOM
// calls and textContent, so nothing in the data is ever run as markup.
"use strict";

const REFRESH_MS = 10000;
const LOOKS = [24, 37, 50, 75, 100, 150, 200];
const SVG = "http://www.w3.org/2000/svg";

let report = null;
let lastSeen = ""; // the last data drawn, so an unchanged refresh doesn't redraw
let open = null; // the open cell: {model, kind, task, size}
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

const n = (x) => Number(x || 0).toLocaleString();
const pct = (x) => Math.round(x * 100) + "%";
const plural = (k, one, many) => (k === 1 ? one : many);
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

// shortPace is the speed in a few words, e.g. "20% faster" or "2.9× as long".
function shortPace(c) {
  return c.pace.replace(" than Claude", "").replace("× Claude's time", "× as long").replace("about as fast as Claude", "as fast");
}

// cellLine is the one quiet line under a result.
function cellLine(c) {
  const right = `${c.passed} of ${c.graded} right`;
  switch (c.result) {
    case "local": return c.speed === "ok" ? `${right} · ${shortPace(c)}` : right;
    case "slow": return c.limit === "never local" ? `${right} · your setting` : `${right} · ${shortPace(c)}`;
    case "learning": return c.next ? `${right} · verdict at ${c.next}` : right;
  }
  return right;
}

async function refresh() {
  const live = document.getElementById("live");
  try {
    const res = await fetch("api/report", { cache: "no-store" });
    const body = await res.json();
    if (!res.ok) throw new Error(body.error || res.statusText);
    report = body;
    document.getElementById("error").hidden = true;
    applyTheme(report.theme);
    const seen = JSON.stringify({ ...body, generated: "" });
    if (seen !== lastSeen) {
      lastSeen = seen;
      render();
    }
    live.classList.remove("stale");
    live.textContent = `Live · updated ${new Date(report.generated).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`;
  } catch (err) {
    const box = document.getElementById("error");
    box.hidden = false;
    box.textContent = String(err.message || err).includes("fetch")
      ? "Can't reach htd. Is `htd ui` still running? Start it again, then reload this page."
      : "htd couldn't read your results: " + (err.message || err);
    live.classList.add("stale");
    live.textContent = "Not updating";
  }
}

function render() {
  const r = report;
  document.getElementById("empty").hidden = r.models.length > 0;
  renderTotals(r.totals);
  renderSavings(r.savings);
  document.getElementById("models-wrap").hidden = r.models.length === 0;
  document.getElementById("models").replaceChildren(...r.models.map(renderModel));
  renderOverTime(r);
  renderHandoffs(r.handoffs);
}

const stat = (num, label, sub, good) =>
  el("div", { class: good ? "stat good" : "stat" }, el("div", { class: "n" }, num), el("div", { class: "l" }, label), sub && el("div", { class: "s" }, sub));
const money = (v) => (v >= 100 ? "$" + Math.round(v).toLocaleString() : "$" + v.toFixed(2));

function renderTotals(t) {
  document.getElementById("totals").replaceChildren(
    stat(n(t.go_local), plural(t.go_local, "task size goes local now", "task sizes go local now"), null, t.go_local > 0),
    stat(n(t.graded), "checks and replays graded", `${n(t.models)} ${plural(t.models, "model", "models")} tested`),
    stat(t.last_run ? day(t.last_run) : "-", "last check or replay", "Run htd bench or htd replay for more.")
  );
}

// The Savings tab.
function renderSavings(s) {
  const any = s.answered + s.refused + s.failed > 0;
  document.getElementById("tracking-off").hidden = s.tracking;
  document.getElementById("savings-empty").hidden = any || !s.tracking;
  document.getElementById("savings-totals").hidden = !any;
  const at = s.worth_model ? `at ${s.worth_model} API prices` : "no price known for your Claude model";
  document.getElementById("savings-totals").replaceChildren(
    stat(n(s.answered), [plural(s.answered, "task", "tasks") + " handed off by ", el("code", {}, "ask_local")],
      s.refused ? `${n(s.refused)} more ${plural(s.refused, "was", "were")} kept by Claude: not proven, or too slow` : null, s.answered > 0),
    stat(n(s.tokens_kept), "tokens kept out of Claude's context", s.first ? `since ${day(s.first)}` : null),
    stat(s.worth ? money(s.worth) : "-", "saved, " + at, "On a Pro or Max plan that's usage headroom, not dollars.")
  );

  const card = (title, big, lines) => el("div", { class: "panel estimate" }, el("h3", {}, title), el("div", { class: "big" }, big), lines.map((l) => el("p", {}, l)));
  const est = [];
  if (s.pace) {
    const p = s.pace;
    est.push(card("At your current pace", p.worth ? `~${money(p.worth)} a month` : `~${n(p.tasks)} tasks a month`, [
      `About ${n(p.tasks)} ${plural(p.tasks, "task", "tasks")} and ${n(p.tokens)} tokens a month kept out of Claude.`,
      `Based on your handoffs over the last ${p.days} days.`,
    ]));
  } else {
    est.push(card("At your current pace", "-", [s.tracking ? "No handoffs in the last 30 days to go by." : "Turn on handoff tracking to see this."]));
  }
  if (s.room_status === "reading") {
    est.push(card("Room to grow", "…", ["Reading your Claude Code transcripts to see how much of Claude's file reading your models could take. This can take a minute."]));
  } else if (s.room) {
    const r = s.room;
    est.push(card("Room to grow", r.worth ? `up to ${money(r.worth)} a month` : `up to ${n(r.tasks)} reads a month`, [
      `Claude read about ${n(r.tasks)} ${plural(r.tasks, "file", "files")} a month (${r.sizes}), about ${n(r.tokens)} tokens. Your models go local for: ${r.skills}.`,
      `A ceiling, not a promise: it counts every such read in the last ${r.days} days of transcripts as if it were one of those tasks. Most won't be.`,
    ]));
  } else {
    est.push(card("Room to grow", "-", ["Once a model goes local for a skill (Models tab), htd estimates how much of Claude's file reading it could take."]));
  }
  document.getElementById("estimates-wrap").hidden = false;
  document.getElementById("estimates").replaceChildren(...est);

  const rows = (list) => list.map((x) => el("tr", {}, el("td", {}, x.name), el("td", {}, n(x.tasks)), el("td", {}, n(x.tokens)), el("td", {}, x.worth ? money(x.worth) : "-")));
  document.getElementById("shares-wrap").hidden = s.answered === 0;
  document.getElementById("by-model").replaceChildren(...rows(s.by_model));
  document.getElementById("by-task").replaceChildren(...rows(s.by_task));
}

function renderModel(m) {
  const counts = [];
  for (const [key, word] of [["local", "go local"], ["slow", "too slow"], ["learning", "learning"], ["no", "not good enough"]]) {
    if (m.counts[key]) counts.push(el("span", { class: "pill " + key }, `${m.counts[key]} ${key === "local" && m.counts[key] === 1 ? "goes local" : word}`));
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
      el("summary", {}, "Replayed jobs ", el("span", { class: "sub" }, `· ${m.replays.length} ${plural(m.replays.length, "kind", "kinds")}, your past Claude jobs redone locally`)),
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

function taskTable(m, kind, tasks, title) {
  const sizes = [];
  for (const t of tasks) for (const c of t.cells) if (!sizes.some((s) => s.size === c.size)) sizes.push(c);
  const cap = (s) => s.charAt(0).toUpperCase() + s.slice(1);
  const head = el("tr", {}, el("th", {}, title), sizes.map((s) => el("th", {}, cap(s.size), el("small", {}, s.size_words))));
  const rows = tasks.map((t) => {
    const key = t.type + "|" + (t.vs || "");
    return el("tr", {},
      el("td", { class: "task" }, el("b", {}, t.name), t.vs && el("span", {}, `vs ${t.vs}`)),
      sizes.map((s) => {
        const c = t.cells.find((x) => x.size === s.size);
        return el("td", {}, c && c.result !== "none" ? cellView(m, kind, key, t, c) : el("span", { class: "dash", "aria-label": "not run" }, "—"));
      })
    );
  });
  return el("div", { class: "scroll" }, el("table", {}, el("thead", {}, head), el("tbody", {}, rows)));
}

function cellView(m, kind, key, t, c) {
  const isOpen = open && open.model === m.id && open.kind === kind && open.task === key && open.size === c.size;
  return el("button", {
    class: "cell", type: "button", "aria-expanded": isOpen ? "true" : "false",
    "aria-label": `${t.name}, ${c.size}: ${PILL[c.result]}. ${cellLine(c)}. Show details.`,
    onclick: () => {
      open = isOpen ? null : { model: m.id, kind, task: key, size: c.size };
      render();
      const panel = open && document.querySelector(".detail");
      if (panel) panel.scrollIntoView({ block: "nearest", behavior: "smooth" });
    },
  },
    el("span", { class: "pill " + c.result }, PILL[c.result] || c.result),
    el("span", { class: "line" }, cellLine(c))
  );
}

function standing(c, kind) {
  switch (c.result) {
    case "local": return "Goes local: accurate, and fast enough for your speed limit.";
    case "slow": return c.limit === "never local"
      ? "Accurate, but your settings keep it on Claude."
      : `Accurate, but too slow: ${c.pace} (your ${c.limit}).`;
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
  const facts = [
    ["Result", standing(c, kind)],
    ["Accuracy", el("span", { class: "acc" }, el("span", { class: "bar" }, fill, mark), `${c.passed} of ${c.graded} right (${pct(rate)}), ${pct(c.bar)} needed`)],
    ["Speed", kind === "skills"
      ? `${secs(c.secs)} each; Claude takes about ${secs(c.claude_secs)}. Your ${c.limit}.`
      : `${secs(c.secs)} each; ${c.pace} on the same jobs. Your ${c.limit}.`],
  ];
  if (c.skipped) facts.push(["Not graded", `${c.skipped} couldn't be graded and don't count`]);
  if (c.last) facts.push(["Last check", day(c.last)]);
  const close = el("button", { class: "close", type: "button", onclick: () => { open = null; render(); } }, "Close");
  return el("div", { class: "detail" },
    el("div", {},
      el("h4", {}, `${task.name}${task.vs ? " vs " + task.vs : ""} · ${c.size_words}`),
      trendChart(c),
      el("p", {}, `Solid line: share right so far. Dotted: how sure htd is that it's right often enough. It's proven when the dotted line reaches the ${pct(c.bar)} line at a check-in (${LOOKS.join(", ")} checks).`)
    ),
    el("div", {}, el("div", { class: "head-row" }, el("h4", {}, "Where it stands"), close),
      el("dl", {}, facts.map(([k, v]) => [el("dt", {}, k), el("dd", {}, v)])))
  );
}

function trendChart(c) {
  const pts = c.trend || [];
  if (!pts.length) return el("p", {}, "No graded checks yet.");
  const W = 440, H = 170, L = 36, R = 10, T = 12, B = 28;
  const maxN = Math.max(pts[pts.length - 1].n, 2);
  const x = (k) => L + ((k - 1) / (maxN - 1)) * (W - L - R);
  const y = (v) => T + (1 - v) * (H - T - B);
  const s = svgEl("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": `Share right after each check, against the ${pct(c.bar)} bar` });
  for (const v of [0, 0.5, 1]) {
    s.append(svgEl("line", { class: "grid", x1: L, x2: W - R, y1: y(v), y2: y(v) }));
    s.append(svgEl("text", { x: L - 6, y: y(v) + 4, "text-anchor": "end" }, pct(v)));
  }
  for (const look of LOOKS) {
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

function renderOverTime(r) {
  const section = document.getElementById("over-time");
  section.hidden = r.days.length === 0 || r.models.length === 0;
  if (section.hidden) return;
  const W = 900, H = 190, L = 28, R = 12, T = 12, B = 30;
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
    const pts = [];
    days.forEach((d, i) => {
      if (i > 0) pts.push(`${x(i).toFixed(1)},${y(days[i - 1].local[m.id] || 0).toFixed(1)}`);
      pts.push(`${x(i).toFixed(1)},${y(d.local[m.id] || 0).toFixed(1)}`);
    });
    const lastV = days[days.length - 1].local[m.id] || 0;
    pts.push(`${(W - R).toFixed(1)},${y(lastV).toFixed(1)}`);
    s.append(svgEl("polyline", { points: pts.join(" "), fill: "none", stroke: color, "stroke-width": 2.25, "stroke-linejoin": "round", "stroke-dasharray": k % 2 ? "6 4" : "none" }));
    const sw = el("span", { class: "swatch" });
    sw.style.background = color;
    legend.push(el("span", {}, sw, `${m.name}${m.detail.includes("thinking off") ? " (thinking off)" : ""}: ${lastV} now`));
  });
  const every = Math.ceil(days.length / 6);
  days.forEach((d, i) => {
    if (i % every === 0 || i === days.length - 1) s.append(svgEl("text", { x: x(i) + 2, y: H - 8 }, dateOnly(d.date)));
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
    el("td", {}, h.model || "-"),
    el("td", {}, secs(h.secs)),
    el("td", {}, outcome(h))
  )));
}

// Tabs: Savings, Models and Settings. The address bar names the open one
// (#models), so a reload or a bookmark opens it again.
const TABS = ["savings", "models", "settings"];
function showTab(name, focus) {
  if (!TABS.includes(name)) name = "savings";
  for (const t of TABS) {
    const tab = document.getElementById("tab-" + t);
    const on = t === name;
    tab.setAttribute("aria-selected", on ? "true" : "false");
    tab.tabIndex = on ? 0 : -1;
    document.getElementById("panel-" + t).hidden = !on;
    if (on && focus) tab.focus();
  }
  if (name === "settings" && window.htdSettings) window.htdSettings.open();
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
  const next = e.key === "ArrowRight" ? (i + 1) % TABS.length : e.key === "ArrowLeft" ? (i + TABS.length - 1) % TABS.length : -1;
  if (next < 0) return;
  e.preventDefault();
  goTab(TABS[next]);
  document.getElementById("tab-" + TABS[next]).focus();
});
window.addEventListener("hashchange", () => showTab(location.hash.slice(1)));

refresh();
setInterval(() => { if (!document.hidden) refresh(); }, REFRESH_MS);
document.addEventListener("visibilitychange", () => { if (!document.hidden) refresh(); });
