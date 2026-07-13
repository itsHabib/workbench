const state = {
  snapshot: null,
  filters: { repository: "all", status: "all", severity: "all" },
  opener: null,
};

const byId = (id) => document.getElementById(id);
const panels = ["runs", "tasks", "pull-requests", "reliability", "tool-health", "sources"];

document.addEventListener("DOMContentLoaded", () => {
  byId("refresh").addEventListener("click", refresh);
  byId("retry").addEventListener("click", refresh);
  byId("clear-filters").addEventListener("click", clearFilters);
  byId("drawer-close").addEventListener("click", closeDrawer);
  byId("drawer").addEventListener("cancel", (event) => {
    event.preventDefault();
    closeDrawer();
  });
  byId("drawer").addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    event.preventDefault();
    closeDrawer();
  });
  byId("drawer").addEventListener("close", restoreDrawerFocus);
  bindFilter("repository");
  bindFilter("status");
  bindFilter("severity");
  renderLoading();
  refresh();
});

function bindFilter(name) {
  byId(`filter-${name}`).addEventListener("change", (event) => {
    state.filters[name] = event.target.value;
    renderSnapshot();
  });
}

function renderLoading() {
  byId("main-content").setAttribute("aria-busy", "true");
  panels.forEach((id) => replaceChildren(byId(id), paragraph("placeholder", `Loading ${id.replace("-", " ")}…`)));
}

async function refresh() {
  const button = byId("refresh");
  button.disabled = true;
  announce("Refreshing snapshot");
  try {
    const receipt = await requestRefresh();
    const snapshot = await waitForSnapshot(receipt.baseline_version);
    state.snapshot = snapshot;
    reconcileFilters(snapshot);
    setDisconnected(false);
    renderSnapshot();
    announce(`Snapshot version ${snapshot.version} loaded`);
  } catch (error) {
    setDisconnected(true);
    announce(`Refresh failed: ${safeError(error)}`);
  } finally {
    button.disabled = false;
  }
}

async function requestRefresh() {
  const token = readCookie("controlroom_csrf");
  if (!token) throw new Error("missing refresh token");
  const response = await fetch("/api/v1/refresh", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Controlroom-CSRF": token },
    body: JSON.stringify({ mode: "demo", trigger: "manual" }),
  });
  if (!response.ok) throw new Error(`refresh returned ${response.status}`);
  return response.json();
}

async function waitForSnapshot(baseline) {
  const delays = [250, 500, 1000, 2000, 1250];
  let lastError = null;
  for (const delay of delays) {
    await pause(delay);
    try {
      const response = await fetch("/api/v1/snapshot", { headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error(`snapshot returned ${response.status}`);
      const snapshot = await response.json();
      if (snapshot.version > baseline) return snapshot;
    } catch (error) {
      lastError = error;
    }
  }
  if (lastError) throw lastError;
  throw new Error("no newer snapshot arrived within 5 seconds");
}

function pause(milliseconds) { return new Promise((resolve) => window.setTimeout(resolve, milliseconds)); }

function readCookie(name) {
  const prefix = `${encodeURIComponent(name)}=`;
  const entry = document.cookie.split("; ").find((value) => value.startsWith(prefix));
  return entry ? decodeURIComponent(entry.slice(prefix.length)) : "";
}

function setDisconnected(disconnected) {
  byId("reconnect").hidden = !disconnected;
  if (!state.snapshot && disconnected) byId("main-content").setAttribute("aria-busy", "false");
}

function announce(message) { byId("refresh-status").textContent = message; }
function safeError(error) { return error instanceof Error ? error.message : "unknown error"; }

function reconcileFilters(snapshot) {
  const values = filterValues(snapshot);
  for (const dimension of Object.keys(values)) {
    if (state.filters[dimension] !== "all" && !values[dimension].includes(state.filters[dimension])) state.filters[dimension] = "all";
    rebuildSelect(dimension, values[dimension]);
  }
}

function filterValues(snapshot) {
  const repository = new Set(snapshot.repositories || []);
  const status = new Set();
  const severity = new Set();
  snapshot.runs.forEach((row) => { if (row.repository) repository.add(row.repository); add(status, row.status, row.liveness); });
  snapshot.tasks.forEach((row) => add(status, row.status, row.liveness));
  snapshot.pull_requests.forEach((row) => { repository.add(row.repository); add(status, row.state, row.review_decision, row.merge_state_status); });
  snapshot.attention.forEach((row) => add(severity, row.category));
  snapshot.reliability.forEach((row) => row.findings.forEach((finding) => add(severity, finding.severity)));
  snapshot.tool_health.forEach((row) => add(severity, row.worst_severity));
  return { repository: sorted(repository), status: sorted(status), severity: sorted(severity) };
}

function add(set, ...values) { values.filter(Boolean).forEach((value) => set.add(value)); }
function sorted(set) { return Array.from(set).sort((left, right) => left.localeCompare(right)); }

function rebuildSelect(dimension, values) {
  const select = byId(`filter-${dimension}`);
  const label = dimension === "repository" ? "All repositories" : dimension === "status" ? "All statuses" : "All severities";
  replaceChildren(select, option("all", label), ...values.map((value) => option(value, value)));
  select.value = state.filters[dimension];
}

function option(value, label) {
  const node = document.createElement("option");
  node.value = value;
  node.textContent = label;
  return node;
}

function clearFilters() {
  state.filters = { repository: "all", status: "all", severity: "all" };
  for (const dimension of Object.keys(state.filters)) byId(`filter-${dimension}`).value = "all";
  renderSnapshot();
}

function renderSnapshot() {
  const snapshot = state.snapshot;
  if (!snapshot) return;
  byId("main-content").setAttribute("aria-busy", "false");
  byId("snapshot-time").textContent = `Generated ${formatTime(snapshot.generated_at)}`;
  byId("snapshot-version").textContent = `Version ${snapshot.version}`;
  byId("source-summary").textContent = sourceSummary(snapshot.sources);
  renderAttention(snapshot);
  renderRuns(snapshot.runs);
  renderTasks(snapshot.tasks);
  renderPullRequests(snapshot.pull_requests);
  renderReliability(snapshot.reliability);
  renderToolHealth(snapshot.tool_health, snapshot.sources);
  renderSources(snapshot.sources);
  reconcileOpenDrawer(snapshot);
}

function sourceSummary(sources) {
  const qualified = sources.filter((source) => source.state !== "ok");
  return qualified.length ? `${sources.length} sources · ${qualified.length} qualified` : `${sources.length} sources current`;
}

function renderAttention(snapshot) {
  const categories = ["urgent", "actionable", "waiting", "informational"];
  const counts = Object.fromEntries(categories.map((category) => [category, snapshot.attention.filter((item) => item.category === category).length]));
  replaceChildren(byId("attention-counts"), ...categories.map((category) => textNode("span", "count", `${category} ${counts[category]}`)));
  const promoted = snapshot.attention.filter((item) => ["urgent", "actionable", "waiting"].includes(item.category)).filter(matchesAttention).slice(0, 3);
  const target = byId("attention-list");
  if (promoted.length === 0) {
    replaceChildren(target, paragraph("empty", anyFilter() ? "Nothing matches the current filters" : "Nothing urgent in this snapshot"));
    return;
  }
  replaceChildren(target, ...promoted.map(attentionCard));
}

function attentionCard(item) {
  const card = document.createElement("article");
  card.className = "attention-card";
  card.dataset.category = item.category;
  card.append(
    textNode("p", "score", `${item.category} · score ${item.score}`),
    textNode("h3", "", item.title),
    textNode("p", "policy", `${item.rule_id} · ${item.id}`),
    textNode("p", "", item.reason),
    textNode("p", "", [item.repository, item.project].filter(Boolean).join(" · ") || "Portfolio-wide"),
    linkList(item.links),
  );
  return card;
}

function matchesAttention(item) {
  return matchesRepository(item.repository) && matchesSeverity(item.category);
}

function renderRuns(rows) {
  renderCollection("runs", rows.filter((row) => matchesRepository(row.repository) && matchesStatus(row.status, row.liveness)), "runs", runRow);
}

function runRow(run) {
  return actionRow(`Open run ${run.id}`, () => openRun(run), [
    ["Run", `${run.kind} · ${run.id}`], ["Repository / project", run.repository || run.project || "Unknown"],
    ["Producer status", run.status], ["Control Room policy", run.liveness || "unknown"], ["Phase", run.phase || "Unknown"],
    ["Runtime", availability(run.actual.runtime)], ["Age", age(run.updated_at)], ["Failure", run.failure || "None"],
  ]);
}

function renderTasks(rows) {
  renderCollection("tasks", rows.filter((row) => matchesStatus(row.status, row.liveness)), "tasks", (task) => staticRow([
    ["Task", `${task.title} · ${task.slug}`], ["Project / phase", [task.project, task.phase].filter(Boolean).join(" · ")],
    ["Dossier status", task.status], ["Control Room policy", task.liveness || "unknown"], ["Assignee", task.assignee || "Unassigned"],
    ["Dependencies", listText(task.dependencies)], ["Blockers", listText(task.blockers)], ["Artifacts", linksText(task.artifacts)],
  ]));
}

function renderPullRequests(rows) {
  renderCollection("pull-requests", rows.filter((row) => matchesRepository(row.repository) && matchesStatus(row.state, row.review_decision, row.merge_state_status)), "pull requests", (pr) => actionRow(`Open pull request ${pr.repository} number ${pr.number}`, () => openPullRequest(pr), [
    ["Pull request", `${pr.repository}#${pr.number} · ${pr.title}`], ["State", `${pr.draft ? "draft · " : ""}${pr.state}`],
    ["Branch", `${pr.head || "Unknown"} → ${pr.base || "Unknown"}`], ["Age", age(pr.created_at)], ["Checks", checksText(pr.checks)],
    ["Review", pr.review_decision || "Unknown"], ["Threads", String(pr.unresolved_threads)], ["Merge", `${pr.mergeable || "Unknown"} · ${pr.merge_state_status || "Unknown"}`],
    ["Next condition", pr.next_condition || "Unknown"],
  ]));
}

function renderReliability(rows) {
  const filtered = rows.filter((diagnosis) => diagnosis.findings.some((finding) => matchesSeverity(finding.severity)) || state.filters.severity === "all");
  renderCollection("reliability", filtered, "reliability records", (diagnosis) => staticRow([
    ["Run", diagnosis.run_id], ["Verdict", `${diagnosis.verdict} · ${diagnosis.tier} · ${diagnosis.dialect}`],
    ["Findings", `${diagnosis.findings.length} · highest ${highestSeverity(diagnosis.findings)}`],
    ["Input tokens", availability(diagnosis.input_tokens)], ["Output tokens", availability(diagnosis.output_tokens)],
    ["Cost", availability(diagnosis.cost_usd)], ["Latency", availability(diagnosis.latency_ms)],
  ]));
}

function renderToolHealth(rows, sources) {
  const source = sources.find((item) => item.source === "toolhealth");
  const filtered = rows.filter((row) => matchesSeverity(row.worst_severity));
  renderCollection("tool-health", filtered, "tool-health records", (health) => staticRow([
    ["Tool", health.tool], ["Worst severity", health.worst_severity || "Unknown"], ["Recurrence", `${health.session_count} sessions`],
    ["Last occurrence", `${formatTime(health.last_occurrence)} · ${age(health.last_occurrence)}`], ["Pain", listText(health.pain)],
    ["Label", health.kind === "accumulated_friction" ? "Accumulated friction" : health.kind], ["Freshness", health.stale || source?.state === "stale" ? "Stale retained data" : "Current"],
  ]));
}

function renderSources(rows) {
  renderCollection("sources", rows, "source receipts", (source) => staticRow([
    ["Source", source.source], ["State", source.state], ["Observed", formatTime(source.observed_at)], ["Duration", `${source.duration_ms} ms`],
    ["Code", source.error_code || "None"], ["Message", source.message || "None"], ["Usable", sourceUsability(source.state)],
  ]));
}

function renderCollection(id, rows, noun, renderer) {
  const target = byId(id);
  if (rows.length === 0) {
    replaceChildren(target, paragraph("empty", anyFilter() ? `No ${noun} match the current filters. Clear filters to restore the full snapshot.` : `No ${noun} in this snapshot.`));
    return;
  }
  replaceChildren(target, ...rows.map(renderer));
}

function staticRow(cells) {
  const row = document.createElement("article");
  row.className = "row";
  row.append(...cells.map(([label, value]) => cell(label, value)));
  return row;
}

function actionRow(label, action, cells) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "row row-action";
  button.setAttribute("aria-label", label);
  button.addEventListener("click", action);
  button.append(...cells.map(([cellLabel, value]) => cell(cellLabel, value)));
  return button;
}

function cell(label, value) {
  const wrapper = document.createElement("span");
  wrapper.className = "cell";
  wrapper.append(textNode("span", "cell-label", label), textNode("span", `cell-value ${statusClass(value)}`, String(value || "Unknown")));
  return wrapper;
}

function statusClass(value) {
  const normalized = String(value).toLowerCase();
  for (const [needle, suffix] of [["on_fire", "on-fire"], ["failed", "failed"], ["urgent", "urgent"], ["blocked", "blocked"], ["actionable", "actionable"], ["live", "live"], ["ready", "ready"], ["ok", "ok"], ["running", "running"], ["waiting", "waiting"]]) {
    if (normalized.includes(needle)) return `status status-${suffix}`;
  }
  return "";
}

function matchesRepository(value) { return state.filters.repository === "all" || value === state.filters.repository; }
function matchesStatus(...values) { return state.filters.status === "all" || values.includes(state.filters.status); }
function matchesSeverity(value) { return state.filters.severity === "all" || value === state.filters.severity; }
function anyFilter() { return Object.values(state.filters).some((value) => value !== "all"); }

function availability(field) {
  if (!field || field.state === "unknown") return "Unknown";
  if (field.state === "unavailable") return "Unavailable";
  return field.value === undefined || field.value === null ? "Unknown" : String(field.value);
}

function formatTime(value) {
  if (!value || value.startsWith("0001-")) return "Unknown";
  return new Date(value).toISOString().replace(".000Z", "Z");
}

function age(value) {
  if (!value || !state.snapshot) return "Unknown";
  const milliseconds = Math.max(0, Date.parse(state.snapshot.generated_at) - Date.parse(value));
  const minutes = Math.floor(milliseconds / 60000);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function listText(values) { return values && values.length ? values.join(", ") : "None"; }
function linksText(values) { return values && values.length ? values.map((link) => link.label || link.url || link.path).join(", ") : "None"; }
function checksText(checks) { return checks && checks.length ? checks.map((check) => `${check.name}: ${check.conclusion || check.status}`).join(", ") : "Unknown"; }
function highestSeverity(findings) { return findings.map((finding) => finding.severity).filter(Boolean)[0] || "Unknown"; }
function sourceUsability(value) { return value === "unavailable" ? "Other source panels remain usable" : value === "stale" ? "Retained rows remain visible with stale qualification" : value === "degraded" ? "Qualified data remains usable" : "Current source facts are usable"; }

function openRun(run) {
  openDrawer("Run details", "Control Room policy + producer facts", [
    ["ID", run.id], ["Kind", run.kind], ["Repository", run.repository || "Unknown"], ["Project", run.project || "Unknown"],
    ["Producer status", run.status], ["Control Room policy liveness", run.liveness || "Unknown"], ["Phase", run.phase || "Unknown"],
    ["Requested runtime", availability(run.requested.runtime)], ["Requested provider", availability(run.requested.provider)],
    ["Actual runtime", availability(run.actual.runtime)], ["Actual provider", availability(run.actual.provider)],
    ["Failure", run.failure || "None"], ["Evidence", linksText(run.evidence)],
  ], { type: "run", id: run.id });
}

function openPullRequest(pr) {
  openDrawer("Pull request details", "GitHub normalized facts", [
    ["Pull request", `${pr.repository}#${pr.number}`], ["Title", pr.title], ["State", `${pr.draft ? "Draft · " : ""}${pr.state}`],
    ["Branch", `${pr.head || "Unknown"} → ${pr.base || "Unknown"}`], ["Checks", checksText(pr.checks)], ["Review decision", pr.review_decision || "Unknown"],
    ["Unresolved threads", String(pr.unresolved_threads)], ["Merge facts", `${pr.mergeable || "Unknown"} · ${pr.merge_state_status || "Unknown"}`],
    ["Detail state", `${pr.detail_state}${pr.truncated_connections?.length ? ` · truncated: ${pr.truncated_connections.join(", ")}` : ""}`],
    ["Next factual condition", pr.next_condition || "Unknown"], ["Link", { label: "Open on GitHub", url: pr.url }],
  ], { type: "pr", id: pr.id });
}

function openDrawer(title, kicker, rows, entity) {
  state.opener = document.activeElement;
  const drawer = byId("drawer");
  drawer.dataset.entityType = entity.type;
  drawer.dataset.entityId = entity.id;
  byId("drawer-title").textContent = title;
  byId("drawer-kicker").textContent = kicker;
  const list = document.createElement("dl");
  rows.forEach(([label, value]) => {
    list.append(textNode("dt", "", label));
    const detail = document.createElement("dd");
    if (value && typeof value === "object") detail.append(safeLink(value));
    else detail.textContent = String(value);
    list.append(detail);
  });
  replaceChildren(byId("drawer-body"), list);
  drawer.showModal();
  byId("drawer-close").focus();
}

function closeDrawer() { byId("drawer").close(); }
function restoreDrawerFocus() { if (state.opener && state.opener.isConnected) state.opener.focus(); }

function reconcileOpenDrawer(snapshot) {
  const drawer = byId("drawer");
  if (!drawer.open) return;
  const collection = drawer.dataset.entityType === "run" ? snapshot.runs : snapshot.pull_requests;
  if (!collection.some((entity) => entity.id === drawer.dataset.entityId)) closeDrawer();
}

function safeLink(link) {
  if (link.url && link.url.startsWith("https://")) {
    const anchor = document.createElement("a");
    anchor.className = "safe-link";
    anchor.href = link.url;
    anchor.target = "_blank";
    anchor.rel = "noreferrer noopener";
    anchor.textContent = link.label || "Open evidence";
    return anchor;
  }
  const code = document.createElement("code");
  code.textContent = link.path || link.label || "Unavailable";
  return code;
}

function linkList(links) {
  const wrapper = document.createElement("div");
  (links || []).forEach((link) => wrapper.append(safeLink(link), document.createTextNode(" ")));
  return wrapper;
}

function paragraph(className, value) { return textNode("p", className, value); }
function textNode(tag, className, value) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  node.textContent = value;
  return node;
}
function replaceChildren(target, ...children) { target.replaceChildren(...children); }
