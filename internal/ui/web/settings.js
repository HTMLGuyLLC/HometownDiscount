// The Settings tab: a form that writes ~/.htd/config.toml. htd checks every
// change the same way it checks the file when it reads it, and shows the
// file it would save before you save it.
"use strict";

(function () {
  let form = null; // the settings being edited
  let saved = ""; // the settings as last loaded or saved, as JSON
  let version = ""; // which config.toml the form was loaded from; sent back on save
  let opts = null;
  let loading = null;
  let previewTimer = 0;

  const $ = (id) => document.getElementById(id);
  const clone = (x) => JSON.parse(JSON.stringify(x));
  const dirty = () => form && JSON.stringify(form) !== saved;
  const dk = (...parts) => parts.join(".");

  // A cleared number box holds "" until it is filled in again. Saving waits.
  function numberProblem() {
    if (!form) return false;
    if (form.speed_check && form.max_slower === "") return true;
    return form.limits.some((l) => l.rule === "percent" && l.percent === "");
  }

  // What gets sent: the form, with blanks in boxes that aren't in use put
  // back to something htd accepts.
  function payload() {
    const p = clone(form);
    if (!p.speed_check && p.max_slower === "") p.max_slower = JSON.parse(saved).max_slower;
    for (const l of p.limits) if (l.rule !== "percent" && l.percent === "") l.percent = 0;
    return p;
  }

  function problem(node) {
    const box = $("settings-problem");
    box.hidden = !node;
    box.replaceChildren(...(node ? [node] : []));
  }

  async function open() {
    if (loading) return;
    if (form && dirty()) return; // never replace what's being typed
    await load();
  }

  // load fetches the settings and draws the form.
  async function load() {
    if (loading) return loading;
    loading = (async () => {
      try {
        const { ok, body } = await api("api/settings", { cache: "no-store" });
        if (!ok) {
          if (!form) problem(el("span", {}, "Couldn't load your settings: " + (body.error || "unknown error")));
          return;
        }
        version = body.version || "";
        opts = body.options;
        form = body.settings;
        saved = JSON.stringify(form);
        $("config-path").textContent = body.path;
        $("toml-preview").textContent = body.toml || "";
        $("reload").hidden = true;
        problem(body.error ? el("span", {}, el("b", {}, "There's a problem with your settings file. "), body.error) : null);
        draw();
      } catch {
        // api() has already said why in the red box.
        if (!form) problem(el("span", {}, "Couldn't load your settings."));
      } finally {
        loading = null;
      }
    })();
    return loading;
  }

  // changed is called after every edit: it updates the save bar and, a
  // moment later, the preview of the file. A redraw puts focus back on the
  // control you were using, or on the first of fallbackKeys that exists.
  function changed(redraw, fallbackKeys) {
    if (redraw) draw(fallbackKeys);
    else saveBar();
    clearTimeout(previewTimer);
    previewTimer = setTimeout(preview, 300);
  }

  function saveBar() {
    if (numberProblem()) status("Enter a number to be able to save.", "error");
    else status(dirty() ? "Unsaved changes" : "", dirty() ? "pending" : "");
  }

  function status(text, kind) {
    const s = $("save-status");
    s.textContent = text;
    s.className = "save-status " + (kind || "");
    $("save").disabled = !dirty() || numberProblem();
    $("revert").disabled = !dirty();
    $("savebar").classList.toggle("on", dirty() || kind === "error" || kind === "ok");
  }

  async function send(path, withVersion) {
    const headers = { "Content-Type": "application/json" };
    if (withVersion) headers["X-HTD-Version"] = version;
    return api(path, { method: "POST", headers, body: JSON.stringify(payload()) });
  }

  async function preview() {
    if (!form || numberProblem()) return;
    try {
      const { ok, body } = await send("api/settings/preview", false);
      if (body.toml) $("toml-preview").textContent = body.toml;
      if (!ok) status(body.error, "error");
      else if (dirty()) status("Unsaved changes", "pending");
    } catch { /* the save bar says so on save */ }
  }

  async function save() {
    if (numberProblem()) return saveBar();
    try {
      const { ok, status: code, body } = await send("api/settings", true);
      if (code === 409) {
        $("reload").hidden = false;
        return status(body.error || "Your settings file changed since you opened this page.", "error");
      }
      if (!ok) return status(body.error || "Couldn't save.", "error");
      saved = JSON.stringify(form);
      version = body.version || version;
      $("toml-preview").textContent = body.toml;
      $("reload").hidden = true;
      problem(null);
      status("Saved. htd uses it from its next run or call.", "ok");
      if (typeof refresh === "function") refresh(); // e.g. a new theme
    } catch {
      status("Couldn't reach htd. Run `htd ui` again; it opens a fresh page.", "error");
    }
  }

  function revert() {
    form = JSON.parse(saved);
    changed(true, ["m.0.url", "m.add"]); // Undo is disabled now; land on the first control
  }

  // After a conflict: fetch what's on disk now and redraw the form from it.
  async function reload() {
    if (dirty() && !confirm("You have unsaved changes. Reloading will discard them.\n\nReload anyway?")) return;
    form = null; // so load() replaces the form even though it's dirty
    await load();
    if (form) status("Reloaded. Make your change again, then save.", "ok");
  }

  // Building blocks. k is the control's stable key, used to put focus back
  // after the form is redrawn.
  function field(label, control, help) {
    return el("label", { class: "field" }, el("span", { class: "label" }, label), control, help && el("span", { class: "help" }, help));
  }
  function text(obj, key, k, attrs) {
    const i = el("input", { type: "text", value: obj[key] ?? "", spellcheck: "false", "data-key": k, ...attrs });
    i.addEventListener("input", () => { obj[key] = i.value; changed(); });
    return i;
  }
  function number(obj, key, k, attrs) {
    const errId = "err-" + k;
    const i = el("input", { type: "number", value: obj[key] ?? "", inputmode: "decimal", "data-key": k, "aria-describedby": errId, ...attrs });
    const err = el("span", { class: "field-error", id: errId }, "Enter a number");
    const show = () => {
      err.hidden = obj[key] !== "";
      if (obj[key] === "") i.setAttribute("aria-invalid", "true"); else i.removeAttribute("aria-invalid");
    };
    i.addEventListener("input", () => {
      const v = i.value.trim();
      obj[key] = v === "" || !Number.isFinite(Number(v)) ? "" : Number(v);
      show();
      changed();
    });
    show();
    return el("span", { class: "numwrap" }, i, err);
  }
  function select(obj, key, choices, redraw, k) {
    const s = el("select", { "data-key": k }, choices.map(([v, label]) => el("option", { value: v, selected: obj[key] === v }, label)));
    s.addEventListener("change", () => { obj[key] = s.value; changed(redraw); });
    return s;
  }
  function check(obj, key, label, help, k, before) {
    const i = el("input", { type: "checkbox", checked: !!obj[key], "data-key": k });
    i.addEventListener("change", () => {
      if (before && !before(i.checked)) { i.checked = !i.checked; return; }
      obj[key] = i.checked;
      changed(true);
    });
    return el("label", { class: "check" }, i, el("span", {}, el("span", { class: "label" }, label), help && el("span", { class: "help" }, help)));
  }
  function radios(obj, key, choices, groupLabel) {
    const name = "r-" + key;
    return el("div", { class: "radios", role: "radiogroup", "aria-label": groupLabel }, choices.map(([v, label, help]) => {
      const i = el("input", { type: "radio", name, value: v, checked: obj[key] === v, "data-key": dk("r", key, v) });
      i.addEventListener("change", () => { obj[key] = v; changed(true); });
      return el("label", { class: "check" }, i, el("span", {}, el("span", { class: "label" }, label), help && el("span", { class: "help" }, help)));
    }));
  }
  function button(label, onclick, cls, k, aria) {
    return el("button", { type: "button", class: "btn " + (cls || ""), onclick, "data-key": k, "aria-label": aria }, label);
  }
  function section(title, intro, ...body) {
    return el("fieldset", { class: "panel group" }, el("legend", {}, title), intro && el("p", { class: "intro" }, intro), body);
  }
  // move swaps a row with its neighbour and keeps focus on the same button,
  // which now sits on the moved row, so it can be pressed again.
  function move(list, prefix, i, by, which) {
    const j = i + by;
    if (j < 0 || j >= list.length) return;
    [list[i], list[j]] = [list[j], list[i]];
    changed(true, [dk(prefix, j, which)]);
  }

  function draw(fallbackKeys) {
    const key = focusKey();
    const f = form;
    const skillChoices = opts.skills.map((c) => [c.value, c.label]);
    const taskChoices = opts.tasks.map((c) => [c.value, c.label]);
    const isSkill = (t) => opts.skills.some((c) => c.value === t);
    const sizeChoices = (task) => [["", "Any size"], ...(isSkill(task) ? opts.skill_sizes : opts.replay_sizes).map((s) => [s, s.charAt(0).toUpperCase() + s.slice(1)])];
    const modelNames = [...new Set(f.models.map((m) => m.model.trim()).filter(Boolean))];
    const nameOf = (choices, v) => (choices.find(([x]) => x === v) || [v, v || "any"])[1];

    // 1. Local models
    const models = f.models.map((m, i) => {
      const who = `model ${i + 1}: ${m.model || m.url || "not filled in yet"}`;
      return el("div", { class: "row-card" },
        el("div", { class: "row-head" }, el("b", {}, `Model ${i + 1}`),
          el("span", { class: "row-tools" },
            button("Move up", () => move(f.models, "m", i, -1, "up"), "small", dk("m", i, "up"), `Move ${who} up`),
            button("Move down", () => move(f.models, "m", i, 1, "down"), "small", dk("m", i, "down"), `Move ${who} down`),
            button("Remove", () => { f.models.splice(i, 1); changed(true, [dk("m", i, "remove"), dk("m", i - 1, "remove"), "m.add"]); }, "small danger", dk("m", i, "remove"), `Remove ${who}`))),
        el("div", { class: "grid3" },
          field("Server address", text(m, "url", dk("m", i, "url"), { placeholder: "http://127.0.0.1:8000" }), "Any server with an OpenAI-style /v1/chat/completions"),
          field("Model name", text(m, "model", dk("m", i, "model"), { placeholder: "as your server lists it" })),
          field("Quantization", text(m, "quant", dk("m", i, "quant"), { placeholder: "auto" }), "auto detects it; or e.g. 4bit, Q4_K_M")),
        check(m, "allow_remote", "This server is off my computer and local network",
          "Only for a server you run elsewhere on purpose. Replays and ask_local would send your code there.",
          dk("m", i, "remote"),
          (on) => !on || confirm("Allow a server off this computer and your local network?\n\nReplays and ask_local would send your code to it. Only do this for a server you control."))
      );
    });
    const modelsSection = section("Local models",
      "The models htd checks and hands work to. List smaller models first: when two are equally fast, the first one listed gets the task.",
      models.length ? models : el("p", { class: "empty-row" }, "No local models yet. Add one, or run htd setup in a terminal to find yours."),
      button("+ Add a model", () => { f.models.push({ url: "http://127.0.0.1:8000", model: "", quant: "auto", loop: "builtin", allow_remote: false }); changed(true, [dk("m", f.models.length - 1, "url")]); }, "", "m.add"));

    // 2. Which model takes a task
    const pins = f.pins.map((p, i) => el("div", { class: "row-line" },
      field("Skill", select(p, "skill", skillChoices, false, dk("pin", i, "skill"))),
      field("Size", select(p, "size", sizeChoices(p.skill), false, dk("pin", i, "size"))),
      field("Model", select(p, "model", (modelNames.includes(p.model) || !p.model ? modelNames : [...modelNames, p.model]).map((x) => [x, x]), false, dk("pin", i, "model"))),
      button("Remove", () => { f.pins.splice(i, 1); changed(true, [dk("pin", i, "remove"), dk("pin", i - 1, "remove"), "pin.add"]); }, "small danger", dk("pin", i, "remove"),
        `Remove pinned task ${i + 1}: ${nameOf(skillChoices, p.skill)} to ${p.model || "no model"}`)));
    const pickSection = section("Which model takes a task",
      "When more than one model has proven a task and is fast enough for it.",
      radios(f, "prefer", [["fastest", "The fastest", "Quickest at that size; a tie goes to the one listed first."], ["listed", "The first in my list", "Order your models above."]], "Which model takes a task"),
      el("h4", {}, "Always use a certain model for…"),
      pins.length ? pins : el("p", { class: "empty-row" }, "No pinned tasks."),
      modelNames.length ? button("+ Pin a task to a model", () => { f.pins.push({ skill: opts.skills[0].value, size: "", model: modelNames[0] }); changed(true, [dk("pin", f.pins.length - 1, "skill")]); }, "", "pin.add") : null);

    // 3. Speed
    const auto = f.claude_model === "auto";
    const claudeChoice = { mode: auto ? "auto" : "set" };
    const claudeSel = select(claudeChoice, "mode", [["auto", "The Claude model I used most in the last 3 days"], ["set", "A specific Claude model"]], false, "f.claude_mode");
    claudeSel.addEventListener("change", () => { f.claude_model = claudeChoice.mode === "auto" ? "auto" : ""; changed(true); });
    for (const l of f.limits) if (!sizeChoices(l.task).some(([v]) => v === l.size)) l.size = "";
    const ruleChoices = [["percent", "Up to a percent slower"], ["always", "Always local once accurate"], ["never", "Never local"]];
    const limits = f.limits.map((l, i) => el("div", { class: "row-line" },
      field("Task", select(l, "task", taskChoices, true, dk("limit", i, "task"))),
      field("Size", select(l, "size", sizeChoices(l.task), false, dk("limit", i, "size"))),
      field("Rule", select(l, "rule", ruleChoices, true, dk("limit", i, "rule"))),
      l.rule === "percent" ? field("Percent slower", number(l, "percent", dk("limit", i, "percent"), { min: 0, step: 1 })) : el("span"),
      button("Remove", () => { f.limits.splice(i, 1); changed(true, [dk("limit", i, "remove"), dk("limit", i - 1, "remove"), "limit.add"]); }, "small danger", dk("limit", i, "remove"),
        `Remove limit ${i + 1}: ${nameOf(taskChoices, l.task)}, ${nameOf(ruleChoices, l.rule).toLowerCase()}`)));
    const speedSection = section("Speed",
      "A task a model has proven goes local only if it's not much slower than Claude.",
      check(f, "speed_check", "Check speed", "Off: accuracy alone decides.", "f.speed_check"),
      f.speed_check ? field("Go local if it's at most this many percent slower than Claude", number(f, "max_slower", "f.max_slower", { min: 0, step: 1 })) : null,
      field("Compare with", claudeSel),
      auto ? null : field("Claude model", text(f, "claude_model", "f.claude_model", { placeholder: "claude-opus-5-5" })),
      el("h4", {}, "Different limits for some tasks"),
      limits.length ? limits : el("p", { class: "empty-row" }, "None: every task uses the limit above."),
      button("+ Add a limit", () => { f.limits.push({ task: opts.tasks[0].value, size: "", rule: "percent", percent: f.max_slower || 20 }); changed(true, [dk("limit", f.limits.length - 1, "task")]); }, "", "limit.add"));

    // 4. Privacy
    const privacySection = section("Privacy",
      "Everything htd keeps stays in ~/.htd on this computer.",
      check(f, "track_handoffs", "Keep a log of ask_local handoffs",
        "Task, file size, model and result for each call, never file contents. The Savings tab is built from it. Off: nothing is logged from now on (htd forget deletes what's there).",
        "f.track_handoffs"));

    // 5. Appearance
    const lookSection = section("Appearance", null,
      radios(f, "theme", [["auto", "Match my computer", "Light or dark, as your system is set."], ["light", "Light"], ["dark", "Dark"]], "Theme"));

    $("settings").replaceChildren(modelsSection, pickSection, speedSection, privacySection, lookSection);
    saveBar();
    // The keys a caller names come first (the moved row's button, say);
    // otherwise the control that had focus.
    if (fallbackKeys) restoreFocus(fallbackKeys[0], [...fallbackKeys.slice(1), key]);
    else if (key) restoreFocus(key);
  }

  $("save").addEventListener("click", save);
  $("revert").addEventListener("click", revert);
  $("reload").addEventListener("click", reload);
  window.addEventListener("beforeunload", (e) => { if (dirty()) e.preventDefault(); });
  window.htdSettings = { open };
  showTab(location.hash.slice(1)); // app.js draws the tabs; this opens the right one now both scripts are in
})();
