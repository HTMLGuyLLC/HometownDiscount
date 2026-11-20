// The Settings tab: a form that writes ~/.htd/config.toml. htd checks every
// change the same way it checks the file when it reads it, and shows the
// file it would save before you save it.
"use strict";

(function () {
  let form = null; // the settings being edited
  let saved = ""; // the settings as last loaded or saved, as JSON
  let token = "";
  let opts = null;
  let loading = null;
  let previewTimer = 0;

  const $ = (id) => document.getElementById(id);
  const clone = (x) => JSON.parse(JSON.stringify(x));
  const dirty = () => form && JSON.stringify(form) !== saved;

  async function open() {
    if (form || loading) return;
    loading = fetch("api/settings", { cache: "no-store" }).then((r) => r.json()).then((body) => {
      token = body.token;
      opts = body.options;
      form = body.settings;
      saved = JSON.stringify(form);
      $("config-path").textContent = body.path;
      $("toml-preview").textContent = body.toml || "";
      const problem = $("settings-problem");
      problem.hidden = !body.error;
      problem.textContent = body.error || "";
      draw();
    }).catch(() => {
      $("settings-problem").hidden = false;
      $("settings-problem").textContent = "Couldn't load your settings. Is `htd ui` still running?";
    }).finally(() => { loading = null; });
  }

  // changed is called after every edit: it updates the save bar and, a
  // moment later, the preview of the file.
  function changed(redraw) {
    if (redraw) draw();
    status(dirty() ? "Unsaved changes" : "", dirty() ? "pending" : "");
    clearTimeout(previewTimer);
    previewTimer = setTimeout(preview, 300);
  }

  function status(text, kind) {
    const s = $("save-status");
    s.textContent = text;
    s.className = "save-status " + (kind || "");
    $("save").disabled = !dirty();
    $("revert").disabled = !dirty();
    $("savebar").classList.toggle("on", dirty() || kind === "error" || kind === "ok");
  }

  async function send(path) {
    const res = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-HTD-Token": token },
      body: JSON.stringify(form),
    });
    return [res.ok, await res.json()];
  }

  async function preview() {
    if (!form) return;
    try {
      const [ok, body] = await send("api/settings/preview");
      if (body.toml) $("toml-preview").textContent = body.toml;
      if (!ok) status(body.error, "error");
      else if (dirty()) status("Unsaved changes", "pending");
    } catch { /* the save bar says so on save */ }
  }

  async function save() {
    try {
      const [ok, body] = await send("api/settings");
      if (!ok) return status(body.error || "Couldn't save.", "error");
      saved = JSON.stringify(form);
      $("toml-preview").textContent = body.toml;
      $("settings-problem").hidden = true;
      status("Saved. htd uses it from its next run or call.", "ok");
      if (typeof refresh === "function") refresh(); // e.g. a new theme
    } catch {
      status("Couldn't reach htd. Is `htd ui` still running?", "error");
    }
  }

  function revert() {
    form = JSON.parse(saved);
    changed(true);
  }

  // Building blocks.
  function field(label, control, help) {
    return el("label", { class: "field" }, el("span", { class: "label" }, label), control, help && el("span", { class: "help" }, help));
  }
  function text(obj, key, attrs) {
    const i = el("input", { type: "text", value: obj[key] ?? "", spellcheck: "false", ...attrs });
    i.addEventListener("input", () => { obj[key] = i.value; changed(); });
    return i;
  }
  function number(obj, key, attrs) {
    const i = el("input", { type: "number", value: obj[key] ?? 0, inputmode: "decimal", ...attrs });
    i.addEventListener("input", () => { obj[key] = i.value === "" ? 0 : Number(i.value); changed(); });
    return i;
  }
  function select(obj, key, choices, redraw) {
    const s = el("select", {}, choices.map(([v, label]) => el("option", { value: v, selected: obj[key] === v }, label)));
    s.addEventListener("change", () => { obj[key] = s.value; changed(redraw); });
    return s;
  }
  function check(obj, key, label, help, before) {
    const i = el("input", { type: "checkbox", checked: !!obj[key] });
    i.addEventListener("change", () => {
      if (before && !before(i.checked)) { i.checked = !i.checked; return; }
      obj[key] = i.checked;
      changed(true);
    });
    return el("label", { class: "check" }, i, el("span", {}, el("span", { class: "label" }, label), help && el("span", { class: "help" }, help)));
  }
  function radios(obj, key, choices) {
    const name = "r-" + key;
    return el("div", { class: "radios", role: "radiogroup" }, choices.map(([v, label, help]) => {
      const i = el("input", { type: "radio", name, value: v, checked: obj[key] === v });
      i.addEventListener("change", () => { obj[key] = v; changed(true); });
      return el("label", { class: "check" }, i, el("span", {}, el("span", { class: "label" }, label), help && el("span", { class: "help" }, help)));
    }));
  }
  function button(label, onclick, cls) {
    return el("button", { type: "button", class: "btn " + (cls || ""), onclick }, label);
  }
  function section(title, intro, ...body) {
    return el("fieldset", { class: "panel group" }, el("legend", {}, title), intro && el("p", { class: "intro" }, intro), body);
  }
  function move(list, i, by) {
    const j = i + by;
    if (j < 0 || j >= list.length) return;
    [list[i], list[j]] = [list[j], list[i]];
    changed(true);
  }

  function draw() {
    const f = form;
    const skillChoices = opts.skills.map((c) => [c.value, c.label]);
    const taskChoices = opts.tasks.map((c) => [c.value, c.label]);
    const isSkill = (t) => opts.skills.some((c) => c.value === t);
    const sizeChoices = (task) => [["", "Any size"], ...(isSkill(task) ? opts.skill_sizes : opts.replay_sizes).map((s) => [s, s.charAt(0).toUpperCase() + s.slice(1)])];
    const modelNames = [...new Set(f.models.map((m) => m.model.trim()).filter(Boolean))];

    // 1. Local models
    const models = f.models.map((m, i) => el("div", { class: "row-card" },
      el("div", { class: "row-head" }, el("b", {}, `Model ${i + 1}`),
        el("span", { class: "row-tools" },
          button("Move up", () => move(f.models, i, -1), "small"),
          button("Move down", () => move(f.models, i, 1), "small"),
          button("Remove", () => { f.models.splice(i, 1); changed(true); }, "small danger"))),
      el("div", { class: "grid3" },
        field("Server address", text(m, "url", { placeholder: "http://127.0.0.1:8000" }), "Any server with an OpenAI-style /v1/chat/completions"),
        field("Model name", text(m, "model", { placeholder: "as your server lists it" })),
        field("Quantization", text(m, "quant", { placeholder: "auto" }), "auto detects it; or e.g. 4bit, Q4_K_M")),
      check(m, "allow_remote", "This server is off my computer and local network",
        "Only for a server you run elsewhere on purpose. Replays and ask_local would send your code there.",
        (on) => !on || confirm("Allow a server off this computer and your local network?\n\nReplays and ask_local would send your code to it. Only do this for a server you control."))
    ));
    const modelsSection = section("Local models",
      "The models htd checks and hands work to. List smaller models first: when two are equally fast, the first one listed gets the task.",
      models.length ? models : el("p", { class: "empty-row" }, "No local models yet. Add one, or run htd setup in a terminal to find yours."),
      button("+ Add a model", () => { f.models.push({ url: "http://127.0.0.1:8000", model: "", quant: "auto", loop: "builtin", allow_remote: false }); changed(true); }));

    // 2. Which model takes a task
    const pins = f.pins.map((p, i) => el("div", { class: "row-line" },
      field("Skill", select(p, "skill", skillChoices)),
      field("Size", select(p, "size", sizeChoices(p.skill))),
      field("Model", select(p, "model", (modelNames.includes(p.model) || !p.model ? modelNames : [...modelNames, p.model]).map((x) => [x, x]))),
      button("Remove", () => { f.pins.splice(i, 1); changed(true); }, "small danger")));
    const pickSection = section("Which model takes a task",
      "When more than one model has proven a task and is fast enough for it.",
      radios(f, "prefer", [["fastest", "The fastest", "Quickest at that size; a tie goes to the one listed first."], ["listed", "The first in my list", "Order your models above."]]),
      el("h4", {}, "Always use a certain model for…"),
      pins.length ? pins : el("p", { class: "empty-row" }, "No pinned tasks."),
      modelNames.length ? button("+ Pin a task to a model", () => { f.pins.push({ skill: opts.skills[0].value, size: "", model: modelNames[0] }); changed(true); }) : null);

    // 3. Speed
    const auto = f.claude_model === "auto";
    const claudeChoice = { mode: auto ? "auto" : "set" };
    const claudeSel = select(claudeChoice, "mode", [["auto", "The Claude model I used most in the last 3 days"], ["set", "A specific Claude model"]]);
    claudeSel.addEventListener("change", () => { f.claude_model = claudeChoice.mode === "auto" ? "auto" : ""; changed(true); });
    for (const l of f.limits) if (!sizeChoices(l.task).some(([v]) => v === l.size)) l.size = "";
    const limits = f.limits.map((l, i) => el("div", { class: "row-line" },
      field("Task", select(l, "task", taskChoices, true)),
      field("Size", select(l, "size", sizeChoices(l.task))),
      field("Rule", select(l, "rule", [["percent", "Up to a percent slower"], ["always", "Always local once accurate"], ["never", "Never local"]], true)),
      l.rule === "percent" ? field("Percent slower", number(l, "percent", { min: 0, step: 1 })) : el("span"),
      button("Remove", () => { f.limits.splice(i, 1); changed(true); }, "small danger")));
    const speedSection = section("Speed",
      "A task a model has proven goes local only if it's not much slower than Claude.",
      check(f, "speed_check", "Check speed", "Off: accuracy alone decides."),
      f.speed_check ? field("Go local if it's at most this many percent slower than Claude", number(f, "max_slower", { min: 0, step: 1 })) : null,
      field("Compare with", claudeSel),
      auto ? null : field("Claude model", text(f, "claude_model", { placeholder: "claude-opus-5-5" })),
      el("h4", {}, "Different limits for some tasks"),
      limits.length ? limits : el("p", { class: "empty-row" }, "None: every task uses the limit above."),
      button("+ Add a limit", () => { f.limits.push({ task: opts.tasks[0].value, size: "", rule: "percent", percent: f.max_slower || 20 }); changed(true); }));

    // 4. Privacy
    const privacySection = section("Privacy",
      "Everything htd keeps stays in ~/.htd on this computer.",
      check(f, "track_handoffs", "Keep a log of ask_local handoffs",
        "Task, file size, model and result for each call, never file contents. The Savings tab is built from it. Off: nothing is logged from now on (htd forget deletes what's there)."));

    // 5. Appearance
    const lookSection = section("Appearance", null,
      radios(f, "theme", [["auto", "Match my computer", "Light or dark, as your system is set."], ["light", "Light"], ["dark", "Dark"]]));

    $("settings").replaceChildren(modelsSection, pickSection, speedSection, privacySection, lookSection);
    status(dirty() ? "Unsaved changes" : "", dirty() ? "pending" : "");
  }

  $("save").addEventListener("click", save);
  $("revert").addEventListener("click", revert);
  window.addEventListener("beforeunload", (e) => { if (dirty()) e.preventDefault(); });
  window.htdSettings = { open };
  showTab(location.hash.slice(1)); // app.js draws the tabs; this opens the right one now both scripts are in
})();
