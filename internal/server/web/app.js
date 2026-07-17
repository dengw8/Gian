const maxImageBytes = 5 * 1024 * 1024;

const state = {
  apiBase: window.location.origin,
  currentRunId: "",
  runs: [],
  events: [],
  modelLogs: [],
	trace: { available: false, spans: [] },
  traceSelection: null,
	traceDetailTab: "run",
	traceBlockModes: {},
	traceView: "tree",
	traceEvents: { available: false, events: [], nextSequence: 0, diagnostics: null, status: "idle" },
	traceEventSelection: null,
	traceEventSequences: new Set(),
	traceEventSource: null,
	traceEventRetry: null,
	traceFilters: { q: "", kind: "", status: "", limit: 120 },
	traceExpanded: new Set(),
	traceExpansionInitialized: false,
	page: "messages",
	  pendingApproval: null,
  plan: { mode: "off", steps: [] },
  attachments: [],
  activeInspector: "planPanel",
  busy: false,
  streamingAssistant: null,
};

const el = {
  attachmentPreview: document.querySelector("#attachmentPreview"),
	  approvalView: document.querySelector("#approvalView"),
	  approveShellButton: document.querySelector("#approveShellButton"),
	approvalInspectorTab: document.querySelector("#approvalInspectorTab"),
	approvalPendingBadge: document.querySelector("#approvalPendingBadge"),
	  denyShellButton: document.querySelector("#denyShellButton"),
	  cancelShellButton: document.querySelector("#cancelShellButton"),
  copyRunButton: document.querySelector("#copyRunButton"),
	copyTraceSpanButton: document.querySelector("#copyTraceSpanButton"),
	messageComposer: document.querySelector("#messageComposer"),
	messagesPageButton: document.querySelector("#messagesPageButton"),
	messagesWorkspace: document.querySelector("#messagesWorkspace"),
  currentRunId: document.querySelector("#currentRunId"),
  eventStream: document.querySelector("#eventStream"),
  healthStatus: document.querySelector("#healthStatus"),
  historyCount: document.querySelector("#historyCount"),
  historyList: document.querySelector("#historyList"),
  imageButton: document.querySelector("#imageButton"),
  imageInput: document.querySelector("#imageInput"),
  inspectorTabs: Array.from(document.querySelectorAll(".inspector-tab")),
  inspectorViews: Array.from(document.querySelectorAll(".inspector-view")),
  loadRunButton: document.querySelector("#loadRunButton"),
  messageInput: document.querySelector("#messageInput"),
  messageTimeline: document.querySelector("#messageTimeline"),
  modelLogsView: document.querySelector("#modelLogsView"),
  modelProvider: document.querySelector("#modelProvider"),
  newRunButton: document.querySelector("#newRunButton"),
  executePlanButton: document.querySelector("#executePlanButton"),
  planModeToggle: document.querySelector("#planModeToggle"),
  planStatus: document.querySelector("#planStatus"),
  planView: document.querySelector("#planView"),
  refreshEventsButton: document.querySelector("#refreshEventsButton"),
  refreshModelLogsButton: document.querySelector("#refreshModelLogsButton"),
  refreshMessagesButton: document.querySelector("#refreshMessagesButton"),
  refreshRunButton: document.querySelector("#refreshRunButton"),
	refreshTraceButton: document.querySelector("#refreshTraceButton"),
	traceTreeViewButton: document.querySelector("#traceTreeViewButton"),
	traceChatViewButton: document.querySelector("#traceChatViewButton"),
	traceEventsViewButton: document.querySelector("#traceEventsViewButton"),
	traceSearchInput: document.querySelector("#traceSearchInput"),
	traceKindFilter: document.querySelector("#traceKindFilter"),
	traceStatusFilter: document.querySelector("#traceStatusFilter"),
	traceLoadMoreButton: document.querySelector("#traceLoadMoreButton"),
	traceIntegrity: document.querySelector("#traceIntegrity"),
	resetPlanButton: document.querySelector("#resetPlanButton"),
  runIdInput: document.querySelector("#runIdInput"),
  runStatus: document.querySelector("#runStatus"),
  sendButton: document.querySelector("#sendButton"),
  streamButton: document.querySelector("#streamButton"),
  toast: document.querySelector("#toast"),
  traceDetails: document.querySelector("#traceDetails"),
	traceMetadataTab: document.querySelector("#traceMetadataTab"),
	traceRunTab: document.querySelector("#traceRunTab"),
	tracePageButton: document.querySelector("#tracePageButton"),
	traceTree: document.querySelector("#traceTree"),
	traceWorkspace: document.querySelector("#traceWorkspace"),
	traceCompareRunInput: document.querySelector("#traceCompareRunInput"),
	compareTraceButton: document.querySelector("#compareTraceButton"),
	traceCompareView: document.querySelector("#traceCompareView"),
  tokenUsage: document.querySelector("#tokenUsage"),
  updatedAt: document.querySelector("#updatedAt"),
};

function init() {
	const route = readRunRoute();
	state.page = route.page;
  renderHistory();
  bindEvents();
  selectInspector(state.activeInspector);
  checkHealth();
  loadRunHistory();
	setPage(state.page, false);
	if (route.runId) loadRun(route.runId);
}

function bindEvents() {
  el.newRunButton.addEventListener("click", createEmptyRun);
  el.loadRunButton.addEventListener("click", loadRunFromInput);
  el.copyRunButton.addEventListener("click", copyCurrentRunId);
  el.copyTraceSpanButton.addEventListener("click", copySelectedTraceSpan);
	el.traceRunTab.addEventListener("click", () => setTraceDetailTab("run"));
	el.traceMetadataTab.addEventListener("click", () => setTraceDetailTab("metadata"));
	el.messagesPageButton.addEventListener("click", () => setPage("messages", true));
	el.tracePageButton.addEventListener("click", () => setPage("trace", true));
  el.refreshRunButton.addEventListener("click", refreshRunMetadata);
	el.refreshTraceButton.addEventListener("click", () => state.traceView === "events" ? refreshTraceEvents() : refreshTrace());
	el.traceTreeViewButton.addEventListener("click", () => setTraceView("tree"));
	el.traceChatViewButton.addEventListener("click", () => setTraceView("chat"));
	el.traceEventsViewButton.addEventListener("click", () => setTraceView("events"));
	el.traceLoadMoreButton.addEventListener("click", () => { state.traceFilters.limit += 120; refreshTrace(); });
	[el.traceSearchInput, el.traceKindFilter, el.traceStatusFilter].forEach((control) => control.addEventListener("change", refreshTrace));
	el.traceSearchInput.addEventListener("search", refreshTrace);
	el.compareTraceButton.addEventListener("click", compareTraceRuns);
  el.refreshMessagesButton.addEventListener("click", refreshMessages);
  el.refreshEventsButton.addEventListener("click", refreshEvents);
  el.refreshModelLogsButton.addEventListener("click", refreshModelLogs);
  el.imageButton.addEventListener("click", () => el.imageInput.click());
  el.imageInput.addEventListener("change", handleImageSelection);
  el.planModeToggle.addEventListener("change", () => setPlanMode(el.planModeToggle.checked));
  el.executePlanButton.addEventListener("click", executePlan);
  el.resetPlanButton.addEventListener("click", resetPlan);
	  el.approveShellButton.addEventListener("click", () => resolveShellApproval("approve"));
	  el.denyShellButton.addEventListener("click", () => resolveShellApproval("deny"));
	  el.cancelShellButton.addEventListener("click", () => resolveShellApproval("cancel"));
  el.sendButton.addEventListener("click", () => submitTurn(false));
  el.streamButton.addEventListener("click", () => submitTurn(true));
  el.inspectorTabs.forEach((tab, index) => {
    tab.addEventListener("click", () => selectInspector(tab.dataset.inspectorTarget));
    tab.addEventListener("keydown", (event) => handleInspectorTabKeydown(event, index));
  });

  el.messageInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submitTurn(false);
    }
  });

}

function handleInspectorTabKeydown(event, index) {
  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
    return;
  }
  event.preventDefault();
  let nextIndex = index;
  if (event.key === "ArrowLeft") {
    nextIndex = (index - 1 + el.inspectorTabs.length) % el.inspectorTabs.length;
  }
  if (event.key === "ArrowRight") {
    nextIndex = (index + 1) % el.inspectorTabs.length;
  }
  if (event.key === "Home") {
    nextIndex = 0;
  }
  if (event.key === "End") {
    nextIndex = el.inspectorTabs.length - 1;
  }
  const nextTab = el.inspectorTabs[nextIndex];
  nextTab.focus();
  selectInspector(nextTab.dataset.inspectorTarget);
}

function selectInspector(targetId) {
  if (!targetId) {
    return;
  }
  state.activeInspector = targetId;
  el.inspectorTabs.forEach((tab) => {
    const active = tab.dataset.inspectorTarget === targetId;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-selected", String(active));
    tab.tabIndex = active ? 0 : -1;
  });
  el.inspectorViews.forEach((view) => {
    const active = view.id === targetId;
    view.classList.toggle("active", active);
    view.hidden = !active;
  });
}

function apiURL(path) {
  return `${state.apiBase}${path}`;
}

async function requestJSON(path, options = {}) {
  const response = await fetch(apiURL(path), {
    ...options,
    headers: {
      "content-type": "application/json",
      ...(options.headers || {}),
    },
  });
  const text = await response.text();
  const data = text ? JSON.parse(text) : null;
  if (!response.ok) {
	    const error = new Error(data?.error || `${response.status} ${response.statusText}`);
	    error.status = response.status;
	    throw error;
  }
  return data;
}

async function checkHealth() {
  try {
    const data = await requestJSON("/healthz", { method: "GET" });
    el.healthStatus.textContent = data.status || "ok";
  } catch (error) {
    el.healthStatus.textContent = "Error";
    toast(error.message);
  }
}

async function createEmptyRun() {
  await withBusy(async () => {
    const data = await requestJSON("/v1/runs", {
      method: "POST",
      body: JSON.stringify({ plan_mode: el.planModeToggle.checked }),
    });
    selectRun(data.run);
    selectPlan(data.plan);
    state.modelLogs = [];
    renderMessages(data.messages || []);
    renderModelLogs();
	    await Promise.all([refreshEvents(), refreshApproval(), loadRunHistory()]);
    toast("Run created");
  });
}

async function loadRunFromInput() {
  const runId = el.runIdInput.value.trim();
  if (!runId) {
    toast("Run ID is required");
    return;
  }
  await loadRun(runId);
}

async function loadRun(runId) {
  await withBusy(async () => {
    const run = await requestJSON(`/v1/runs/${encodeURIComponent(runId)}`, {
      method: "GET",
    });
    selectRun(run);
		const loads = [refreshMessages(), refreshEvents(), refreshModelLogs(), refreshPlan(), refreshApproval(), loadRunHistory()];
		if (state.page === "trace") loads.push(refreshTrace());
		await Promise.all(loads);
  });
}

async function refreshRunMetadata() {
  if (!state.currentRunId) {
    toast("No run selected");
    return;
  }
  await withBusy(async () => {
    const run = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}`, {
      method: "GET",
    });
    selectRun(run);
    if (run.plan_mode) {
      el.planStatus.textContent = planLabelFromRun(run);
    }
  });
}

async function refreshMessages() {
  if (!state.currentRunId) {
    toast("No run selected");
    return;
  }
  const messages = await requestJSON(
    `/v1/runs/${encodeURIComponent(state.currentRunId)}/messages`,
    { method: "GET" },
  );
  renderMessages(messages || []);
}

async function loadRunHistory() {
  try {
    const data = await requestJSON("/v1/runs", { method: "GET" });
    state.runs = data.runs || [];
    renderHistory();
  } catch (error) {
    toast(`History load failed: ${error.message}`);
  }
}

async function refreshEvents() {
  if (!state.currentRunId) {
    toast("No run selected");
    return;
  }
  const events = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/events`, {
    method: "GET",
  });
  state.events = events || [];
  renderEvents();
}

async function refreshModelLogs() {
  if (!state.currentRunId) {
    toast("No run selected");
    return;
  }
  const logs = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/model-logs`, {
    method: "GET",
  });
  state.modelLogs = logs || [];
  renderModelLogs();
}

async function refreshTrace() {
	if (!state.currentRunId) {
		renderTrace();
		return;
	}
	state.traceFilters.q = el.traceSearchInput.value.trim();
	state.traceFilters.kind = el.traceKindFilter.value;
	state.traceFilters.status = el.traceStatusFilter.value;
	const query = new URLSearchParams({ limit: String(state.traceFilters.limit) });
	if (state.traceFilters.q) query.set("q", state.traceFilters.q);
	if (state.traceFilters.kind) query.set("kind", state.traceFilters.kind);
	if (state.traceFilters.status) query.set("status", state.traceFilters.status);
	const data = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/trace?${query}`, { method: "GET" });
	state.trace = data || { available: false, spans: [] };
	if (state.traceSelection && !(state.trace.spans || []).some((span) => span.id === state.traceSelection)) {
		state.traceSelection = null;
	}
	renderTrace();
}

async function refreshTraceEvents() {
	if (!state.currentRunId) {
		state.traceEvents = { available: false, events: [], nextSequence: 0, diagnostics: null, status: "idle" };
		state.traceEventSequences = new Set();
		renderTrace();
		return;
	}
	stopTraceEventStream();
	state.traceEvents.status = "loading";
	renderTrace();
	try {
		const data = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/execution-events?after_sequence=0&limit=1000`, { method: "GET" });
		state.traceEvents = { ...(data || {}), events: [], nextSequence: data?.next_sequence || 0, status: "ready" };
		state.traceEventSequences = new Set();
		(data?.events || []).forEach(appendTraceEvent);
		state.traceEvents.status = "live";
		connectTraceEventStream();
	} catch (error) {
		state.traceEvents.status = "error";
		toast(`Execution event load failed: ${error.message}`);
	}
	renderTrace();
}

const traceExecutionEventTypes = [
	"run.started", "run.completed", "run.failed", "turn.started", "turn.completed", "turn.failed",
	"iteration.started", "iteration.completed", "model.started", "model.text", "model.tool_call",
	"model.completed", "model.failed", "tool.started", "tool.completed", "tool.failed",
	"approval.requested", "approval.resolved", "error",
];

function connectTraceEventStream() {
	if (state.page !== "trace" || state.traceView !== "events" || !state.currentRunId || !state.traceEvents?.available) return;
	stopTraceEventStream(false);
	const runID = state.currentRunId;
	const source = new EventSource(apiURL(`/v1/runs/${encodeURIComponent(runID)}/execution-events/stream?after_sequence=${state.traceEvents.nextSequence || 0}`));
	state.traceEventSource = source;
	traceExecutionEventTypes.forEach((type) => source.addEventListener(type, (message) => {
		try {
			if (state.currentRunId !== runID) return;
			appendTraceEvent(JSON.parse(message.data));
			if (state.traceView === "events") renderTrace();
		} catch (error) {
			toast(`Invalid execution event: ${error.message}`);
		}
	}));
	source.onerror = () => {
		if (state.traceEventSource !== source) return;
		stopTraceEventStream(false);
		if (state.page !== "trace" || state.traceView !== "events" || state.currentRunId !== runID) return;
		state.traceEvents.status = "reconnecting";
		renderTrace();
		state.traceEventRetry = window.setTimeout(() => {
			state.traceEventRetry = null;
			connectTraceEventStream();
		}, 1200);
	};
}

function stopTraceEventStream(clearStatus = true) {
	if (state.traceEventRetry) {
		window.clearTimeout(state.traceEventRetry);
		state.traceEventRetry = null;
	}
	if (state.traceEventSource) {
		state.traceEventSource.close();
		state.traceEventSource = null;
	}
	if (clearStatus && state.traceEvents?.status === "live") state.traceEvents.status = "ready";
}

function appendTraceEvent(event) {
	if (!event || typeof event.sequence !== "number" || state.traceEventSequences.has(event.sequence)) return;
	state.traceEventSequences.add(event.sequence);
	state.traceEvents.events.push(event);
	state.traceEvents.events.sort((left, right) => left.sequence - right.sequence);
	state.traceEvents.nextSequence = Math.max(state.traceEvents.nextSequence || 0, event.sequence);
}

function readRunRoute() {
	const match = window.location.pathname.match(/^\/app\/runs\/([^/]+)\/(messages|trace)$/);
	return match ? { runId: decodeURIComponent(match[1]), page: match[2] } : { runId: "", page: "messages" };
}

function setPage(page, push) {
	state.page = page === "trace" ? "trace" : "messages";
	const trace = state.page === "trace";
	el.messagesWorkspace.hidden = trace;
	el.messageComposer.hidden = trace;
	el.traceWorkspace.hidden = !trace;
	el.messagesPageButton.classList.toggle("active", !trace);
	el.tracePageButton.classList.toggle("active", trace);
	if (push && state.currentRunId) updateRunRoute();
	if (trace) {
		const refresh = state.traceView === "events" ? refreshTraceEvents() : refreshTrace();
		refresh.catch((error) => toast(`Trace refresh failed: ${error.message}`));
	} else {
		stopTraceEventStream();
	}
}

function updateRunRoute() {
	if (!state.currentRunId) return;
	const path = `/app/runs/${encodeURIComponent(state.currentRunId)}/${state.page}`;
	if (window.location.pathname !== path) window.history.pushState({}, "", path);
}

function renderTrace() {
	el.traceTree.classList.remove("empty-state");
	el.traceTree.textContent = "";
	el.traceDetails.classList.remove("empty-state");
	el.traceDetails.classList.remove("trace-detail-layout");
	el.traceDetails.textContent = "";
	el.traceLoadMoreButton.hidden = true;
	renderTraceIntegrity();
	if (!state.currentRunId) {
		el.copyTraceSpanButton.disabled = true;
		el.traceTree.classList.add("empty-state");
		el.traceTree.textContent = "Select a run to inspect its execution trace.";
		el.traceDetails.classList.add("empty-state");
		el.traceDetails.textContent = "Select a trace node to inspect its details.";
		return;
	}
	if (state.traceView === "events") {
		el.copyTraceSpanButton.disabled = true;
		renderTraceEvents();
		return;
	}
	if (!state.trace?.available) {
		el.copyTraceSpanButton.disabled = true;
		el.traceTree.classList.add("empty-state");
		el.traceTree.textContent = state.trace?.reason || "Trace was not collected for this run.";
		el.traceDetails.classList.add("empty-state");
		el.traceDetails.textContent = "No trace details are available.";
		return;
	}
	if (state.trace.hidden_spans > 0) {
		el.traceLoadMoreButton.hidden = false;
		el.traceLoadMoreButton.textContent = `Show ${state.trace.hidden_spans} more span${state.trace.hidden_spans === 1 ? "" : "s"}`;
	}
	if (state.traceView === "chat") {
		renderTraceChat(state.trace.spans || []);
		const selectedChat = (state.trace.spans || []).find((span) => span.id === state.traceSelection);
		if (selectedChat) renderTraceDetails(selectedChat);
		else {
			el.copyTraceSpanButton.disabled = true;
			el.traceDetails.classList.add("empty-state");
			el.traceDetails.textContent = "Select a trace event to inspect its details.";
		}
		return;
	}
	const byParent = new Map();
	(state.trace.spans || []).forEach((span) => {
		const parent = span.parent_id || "";
		if (!byParent.has(parent)) byParent.set(parent, []);
		byParent.get(parent).push(span);
	});
	byParent.forEach((items) => items.sort((a, b) => (a.sequence || 0) - (b.sequence || 0) || String(a.started_at).localeCompare(String(b.started_at))));
	const roots = (state.trace.spans || []).filter((span) => !span.parent_id || !state.trace.spans.some((other) => other.id === span.parent_id));
	if (!state.traceExpansionInitialized && roots.length) {
		const expandInitialLevels = (span, depth) => {
			const children = byParent.get(span.id) || [];
			if (children.length && depth < 2) state.traceExpanded.add(span.id);
			children.forEach((child) => expandInitialLevels(child, depth + 1));
		};
		roots.forEach((span) => expandInitialLevels(span, 0));
		state.traceExpansionInitialized = true;
	}
	roots.forEach((span) => renderTraceNode(span, byParent, el.traceTree, 0));
	const selected = (state.trace.spans || []).find((span) => span.id === state.traceSelection);
	if (selected) renderTraceDetails(selected);
	else {
		el.copyTraceSpanButton.disabled = true;
		el.traceDetails.classList.add("empty-state");
		el.traceDetails.textContent = "Select a trace node to inspect its details.";
	}
}

function renderTraceNode(span, byParent, parent, depth) {
	const children = byParent.get(span.id) || [];
	const node = document.createElement("div");
	node.className = "trace-node";
	const summary = document.createElement("div");
	summary.className = `trace-summary trace-${span.status || "running"}`;
	const semanticKind = span.component === "graph" ? "graph" : (span.kind || "span");
	if (children.length) {
		const expanded = state.traceExpanded.has(span.id);
		const toggle = document.createElement("button");
		toggle.type = "button";
		toggle.className = "trace-expand-toggle";
		toggle.textContent = expanded ? "▾" : "▸";
		toggle.setAttribute("aria-expanded", String(expanded));
		toggle.setAttribute("aria-label", `${expanded ? "Collapse" : "Expand"} ${span.name || span.kind || "span"}`);
		toggle.title = toggle.getAttribute("aria-label");
		toggle.addEventListener("click", () => {
			if (expanded) state.traceExpanded.delete(span.id);
			else state.traceExpanded.add(span.id);
			renderTrace();
		});
		summary.append(toggle);
	} else {
		const placeholder = document.createElement("span");
		placeholder.className = "trace-expand-placeholder";
		placeholder.setAttribute("aria-hidden", "true");
		summary.append(placeholder);
	}
	const select = document.createElement("button");
	select.type = "button";
	select.className = "trace-select";
	select.setAttribute("aria-label", `View details for ${span.name || span.kind || "span"}`);
	select.innerHTML = `<span class="trace-kind">${escapeHTML(semanticKind)}</span><strong>${escapeHTML(span.name || span.kind || "span")}</strong><span>${escapeHTML(span.status || "running")}</span><span>${formatTraceDuration(span)}</span>${span.usage ? `<span>${escapeHTML(formatUsageSummary(span.usage))}</span>` : ""}${span.error ? `<span class="trace-error">${escapeHTML(span.error)}</span>` : ""}`;
	select.addEventListener("click", () => { selectTraceSpan(span.id); });
	summary.append(select);
	node.append(summary);
	if (children.length) {
		const nested = document.createElement("div");
		nested.className = "trace-children";
		nested.hidden = !state.traceExpanded.has(span.id);
		children.forEach((child) => renderTraceNode(child, byParent, nested, depth + 1));
		node.append(nested);
	}
	parent.append(node);
}

function renderTraceIntegrity() {
	const diagnostics = state.trace?.diagnostics;
	if (!diagnostics) {
		el.traceIntegrity.textContent = "";
		return;
	}
	if (diagnostics.complete) {
		el.traceIntegrity.className = "trace-integrity complete";
		el.traceIntegrity.textContent = `Trace complete · ${state.trace.total_spans || state.trace.spans?.length || 0} spans`;
		return;
	}
	el.traceIntegrity.className = "trace-integrity incomplete";
	el.traceIntegrity.textContent = `Trace may be incomplete · ${diagnostics.dropped_updates || 0} dropped${diagnostics.last_error ? " · write error" : ""}`;
}

function setTraceView(view) {
	state.traceView = ["tree", "chat", "events"].includes(view) ? view : "tree";
	el.traceTreeViewButton.classList.toggle("active", state.traceView === "tree");
	el.traceChatViewButton.classList.toggle("active", state.traceView === "chat");
	el.traceEventsViewButton.classList.toggle("active", state.traceView === "events");
	if (state.traceView === "events") {
		refreshTraceEvents();
	} else {
		stopTraceEventStream();
	}
	renderTrace();
}

function renderTraceEvents() {
	const timeline = state.traceEvents;
	if (!timeline?.available && timeline?.status !== "loading") {
		el.traceTree.classList.add("empty-state");
		el.traceTree.textContent = timeline?.reason || "Execution events were not collected for this run.";
		el.traceDetails.classList.add("empty-state");
		el.traceDetails.textContent = "Select an execution event to inspect its details.";
		return;
	}
	if (timeline.status === "loading") {
		el.traceTree.classList.add("empty-state");
		el.traceTree.textContent = "Loading execution events…";
		return;
	}
	const status = document.createElement("div");
	status.className = `trace-event-connection ${timeline.status || "ready"}`;
	status.textContent = traceEventConnectionLabel(timeline.status);
	el.traceTree.append(status);
	const events = timeline.events || [];
	if (!events.length) {
		const empty = document.createElement("div");
		empty.className = "empty-state";
		empty.textContent = "No execution events have been recorded for this run.";
		el.traceTree.append(empty);
	} else {
		const groups = new Map();
		events.forEach((event) => {
			const key = `${event.turn_id || "run"}:${event.iteration || 0}`;
			if (!groups.has(key)) groups.set(key, []);
			groups.get(key).push(event);
		});
		groups.forEach((items, key) => renderTraceEventGroup(el.traceTree, key, items));
	}
	const selected = events.find((event) => event.sequence === state.traceEventSelection);
	if (selected) renderTraceEventDetails(selected);
	else {
		el.traceDetails.classList.add("empty-state");
		el.traceDetails.textContent = "Select an execution event to inspect its details.";
	}
}

function traceEventConnectionLabel(status) {
	if (status === "live") return "Live · receiving persisted execution events";
	if (status === "reconnecting") return "Reconnecting from the last sequence…";
	if (status === "error") return "Event replay failed. You can refresh to retry.";
	return "Historical replay loaded";
}

function renderTraceEventGroup(parent, key, events) {
	const section = document.createElement("section");
	section.className = "trace-event-group";
	const heading = document.createElement("h3");
	const first = events[0];
	heading.textContent = first.turn_id ? `Turn ${first.turn_id.slice(-8)}${first.iteration ? ` · iteration ${first.iteration}` : ""}` : "Run events";
	section.append(heading);
	events.forEach((event) => {
		const row = document.createElement("button");
		row.type = "button";
		row.className = `trace-event-row trace-${event.status || "running"}`;
		row.classList.toggle("selected", state.traceEventSelection === event.sequence);
		row.innerHTML = `<span class="trace-event-sequence">#${event.sequence}</span><span class="trace-kind">${escapeHTML(event.type || "event")}</span><span class="trace-event-summary">${escapeHTML(traceEventSummary(event))}</span><span class="trace-event-time">${escapeHTML(formatDate(event.occurred_at))}</span>`;
		row.addEventListener("click", () => selectTraceEvent(event.sequence));
		section.append(row);
	});
	parent.append(section);
}

function traceEventSummary(event) {
	const payload = parseTracePayload(event.payload);
	if (event.error) return event.error;
	if (event.type === "model.text") return payload?.delta || "Model output";
	if (event.type === "model.tool_call") return payload?.name ? `Tool call: ${payload.name}` : "Model requested a tool";
	if (payload?.span?.name) return payload.span.name;
	if (payload?.decision) return `Decision: ${payload.decision}`;
	return event.status || "Execution event";
}

function selectTraceEvent(sequence) {
	state.traceEventSelection = sequence;
	renderTrace();
}

function renderTraceEventDetails(event) {
	el.traceDetails.classList.remove("empty-state");
	const content = document.createElement("div");
	content.className = "trace-detail-content";
	const heading = document.createElement("h3");
	heading.textContent = event.type || "Execution event";
	content.append(heading);
	if (event.span_id) {
		const locate = document.createElement("button");
		locate.type = "button";
		locate.className = "trace-event-span-link";
		locate.textContent = "View related span";
		locate.addEventListener("click", async () => {
			setTraceView("tree");
			await selectTraceSpan(event.span_id);
		});
		content.append(locate);
	}
	renderTraceBlock(content, { id: `event_${event.sequence}` }, "Event JSON", event);
	const summary = document.createElement("aside");
	summary.className = "trace-span-summary";
	[["Sequence", String(event.sequence)], ["Status", event.status || "-"], ["Turn", event.turn_id || "-"], ["Iteration", event.iteration || "-"], ["Time", formatDate(event.occurred_at)], ["Span ID", event.span_id || "-"], ["Error", event.error || "-"]].forEach(([label, value]) => {
		const row = document.createElement("div");
		row.innerHTML = `<span>${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong>`;
		summary.append(row);
	});
	el.traceDetails.append(content, summary);
}

function renderTraceChat(spans) {
	const stream = document.createElement("div");
	stream.className = "trace-chat";
	const ordered = [...spans].sort((a, b) => (a.sequence || 0) - (b.sequence || 0) || String(a.started_at).localeCompare(String(b.started_at)));
	for (const span of ordered) {
		if (!["model", "tool", "approval", "turn"].includes(span.kind)) continue;
		const item = document.createElement("article");
		item.className = `trace-chat-item trace-${span.kind} trace-${span.status || "running"}`;
		const title = document.createElement("button");
		title.type = "button";
		title.className = "trace-chat-title";
		title.textContent = `${span.kind === "turn" ? "Turn" : span.kind === "model" ? "Model" : span.name} · ${formatTraceDuration(span)}`;
		title.addEventListener("click", () => selectTraceSpan(span.id));
		item.append(title);
		if (span.kind === "model") {
			const output = parseTracePayload(span.output);
			const calls = output?.message?.tool_calls || [];
			if (calls.length) {
				const callsNode = document.createElement("div");
				callsNode.className = "trace-chat-calls";
				calls.forEach((call) => {
					const callButton = document.createElement("button");
					callButton.type = "button";
					callButton.textContent = `Tool call: ${call.name}`;
					callButton.addEventListener("click", () => selectTraceSpan(`trace_tool_${call.id}`));
					callsNode.append(callButton);
				});
				item.append(callsNode);
			} else {
				appendTraceChatPreview(item, output?.message?.content || "Model completed");
			}
		} else if (span.kind === "tool") {
			const output = parseTracePayload(span.output);
			appendTraceChatPreview(item, output?.content || output?.error || span.error || "Tool started");
		} else if (span.kind === "turn") {
			const input = parseTracePayload(span.input);
			appendTraceChatPreview(item, input?.user_prompt?.content || "Turn started");
		} else {
			appendTraceChatPreview(item, span.status || "approval");
		}
		stream.append(item);
	}
	if (!stream.childElementCount) stream.textContent = "No conversation events match the current trace filter.";
	el.traceTree.append(stream);
}

function appendTraceChatPreview(parent, value) {
	const preview = document.createElement("p");
	preview.textContent = typeof value === "string" ? value : formatTraceText(value);
	parent.append(preview);
}

async function selectTraceSpan(spanID) {
	let span = (state.trace.spans || []).find((item) => item.id === spanID);
	if (!span && state.currentRunId) {
		try {
			const data = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/trace/spans/${encodeURIComponent(spanID)}`, { method: "GET" });
			span = data?.span;
			if (span) state.trace.spans = [...(state.trace.spans || []), span];
		} catch (error) {
			toast(`Trace span load failed: ${error.message}`);
			return;
		}
	}
	if (!span) {
		toast("The linked trace span is not available in this run");
		return;
	}
	state.traceSelection = span.id;
	renderTrace();
}

function renderTraceDetails(span) {
	el.copyTraceSpanButton.disabled = false;
	el.traceDetails.classList.add("trace-detail-layout");
	el.traceRunTab.classList.toggle("active", state.traceDetailTab === "run");
	el.traceMetadataTab.classList.toggle("active", state.traceDetailTab === "metadata");
	el.traceRunTab.setAttribute("aria-selected", String(state.traceDetailTab === "run"));
	el.traceMetadataTab.setAttribute("aria-selected", String(state.traceDetailTab === "metadata"));
	const content = document.createElement("div");
	content.className = "trace-detail-content";
	const summary = renderTraceSummary(span);
	if (state.traceDetailTab === "metadata") {
		const attributes = span.attributes || {};
		const { runtime, ...metadataAttributes } = attributes;
		const metadata = { id: span.id, parent_id: span.parent_id, run_id: span.run_id, turn_id: span.turn_id, kind: span.kind, component: span.component, name: span.name, status: span.status, revision: span.revision, sequence: span.sequence, started_at: span.started_at, completed_at: span.completed_at, duration_ms: span.duration_ms, model_call_id: span.model_call_id, tool_call_id: span.tool_call_id, links: span.links, usage: span.usage, error: span.error, attributes: metadataAttributes };
		renderTraceBlock(content, span, "Metadata", metadata);
		renderTraceBlock(content, span, "Runtime", runtime || {});
		renderRelatedExecutionEvents(content, span);
		el.traceDetails.append(content, summary);
		return;
	}
	if (span.kind === "turn" || span.component === "graph") {
		const input = parseTracePayload(span.input);
		if (input && typeof input === "object" && !Array.isArray(input)) {
			if (Object.prototype.hasOwnProperty.call(input, "system_prompt")) {
				renderTraceBlock(content, span, "System prompt", input.system_prompt);
			}
			if (Object.prototype.hasOwnProperty.call(input, "user_prompt")) {
				renderTraceBlock(content, span, "User prompt", input.user_prompt);
			}
			renderTraceBlock(content, span, "Output", span.output || []);
			renderRelatedExecutionEvents(content, span);
			el.traceDetails.append(content, summary);
			return;
		}
	}
	if (span.kind === "model") {
		renderModelTraceRun(content, span);
	} else if (span.kind === "tool") {
		renderTraceBlock(content, span, "Arguments", span.input || {});
		renderTraceBlock(content, span, span.error ? "Error" : "Result", span.output || span.error || {});
		renderTraceLinks(content, span);
	} else if (span.kind === "approval") {
		renderTraceBlock(content, span, "Approval request", span.input || {});
		renderTraceBlock(content, span, "Decision", span.output || {});
		renderTraceLinks(content, span);
	} else {
		renderTraceBlock(content, span, "Input", span.input || {});
		renderTraceBlock(content, span, "Output", span.output || {});
		renderTraceLinks(content, span);
	}
	renderRelatedExecutionEvents(content, span);
	el.traceDetails.append(content, summary);
}

function renderRelatedExecutionEvents(parent, span) {
	const matches = (event) => event.span_id === span.id || (span.model_call_id && event.model_call_id === span.model_call_id) || (span.tool_call_id && event.tool_call_id === span.tool_call_id);
	const events = (state.traceEvents?.events || []).filter(matches);
	const section = document.createElement("section");
	section.className = "trace-links";
	const heading = document.createElement("h3");
	heading.textContent = "Related execution events";
	section.append(heading);
	if (!events.length) {
		const load = document.createElement("button");
		load.type = "button";
		load.textContent = "Load related events";
		load.addEventListener("click", async () => {
			await refreshTraceEvents();
			const related = (state.traceEvents.events || []).find(matches);
			setTraceView("events");
			if (related) state.traceEventSelection = related.sequence;
			renderTrace();
		});
		section.append(load);
	} else {
		events.forEach((event) => {
			const button = document.createElement("button");
			button.type = "button";
			button.textContent = `#${event.sequence} ${event.type}`;
			button.addEventListener("click", () => {
				state.traceEventSelection = event.sequence;
				setTraceView("events");
			});
			section.append(button);
		});
	}
	parent.append(section);
}

async function setTraceDetailTab(tab) {
	state.traceDetailTab = tab === "metadata" ? "metadata" : "run";
	renderTrace();
}

function renderModelTraceRun(parent, span) {
	const input = parseTracePayload(span.input);
	const output = parseTracePayload(span.output);
	const calls = output?.message?.tool_calls || [];
	if (calls.length) renderTraceBlock(parent, span, "Tool calls", calls);
	if (input?.system_prompt) renderTraceBlock(parent, span, "System prompt", input.system_prompt);
	if (input?.messages) renderTraceBlock(parent, span, "Messages", input.messages);
	renderTraceBlock(parent, span, "Output", output || {});
	renderTraceLinks(parent, span);
}

function renderTraceLinks(parent, span) {
	if (!span.links?.length) return;
	const section = document.createElement("section");
	section.className = "trace-links";
	const heading = document.createElement("h3");
	heading.textContent = "Related spans";
	section.append(heading);
	span.links.forEach((link) => {
		const button = document.createElement("button");
		button.type = "button";
		button.textContent = `${link.type}: ${link.tool_call_id || link.span_id}`;
		button.addEventListener("click", () => selectTraceSpan(link.span_id));
		section.append(button);
	});
	parent.append(section);
}

function renderTraceSummary(span) {
	const aside = document.createElement("aside");
	aside.className = "trace-span-summary";
	const fields = [
		["Status", span.status || "-"],
		["Span ID", span.id || "-"],
		["Type", span.kind || "-"],
		["Latency", formatTraceDuration(span)],
		["Start time", formatDate(span.started_at)],
		["End time", span.completed_at ? formatDate(span.completed_at) : "-"],
		["Tokens", span.usage ? formatUsageSummary(span.usage) : "-"],
		["Run ID", span.run_id || "-"],
		["Turn ID", span.turn_id || "-"],
		["Model call", span.model_call_id || "-"],
		["Tool call", span.tool_call_id || "-"],
		["Error", span.error || "-"],
	];
	fields.forEach(([label, value]) => {
		const row = document.createElement("div");
		const title = document.createElement("span");
		title.textContent = label;
		const body = document.createElement("strong");
		body.textContent = value;
		body.title = value;
		if (label === "Status") body.className = `trace-status trace-${value}`;
		row.append(title, body);
		aside.append(row);
	});
	return aside;
}

async function compareTraceRuns() {
	const otherRunID = el.traceCompareRunInput.value.trim();
	if (!state.currentRunId || !otherRunID) {
		toast("Select a current Run and enter a Run ID to compare");
		return;
	}
	try {
		const comparison = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/trace/compare/${encodeURIComponent(otherRunID)}`, { method: "GET" });
		const rows = [comparison.current, comparison.other].map((item) => `<tr><td>${escapeHTML(item.run_id)}</td><td>${escapeHTML(item.status)}</td><td>${item.total_spans}</td><td>${item.failed_spans}</td><td>${item.duration_ms}ms</td><td>${escapeHTML(formatUsageSummary(item.token_usage || {}))}</td></tr>`).join("");
		el.traceCompareView.innerHTML = `<table><thead><tr><th>Run</th><th>Status</th><th>Spans</th><th>Failed</th><th>Duration</th><th>Tokens</th></tr></thead><tbody>${rows}</tbody></table>`;
	} catch (error) {
		toast(`Trace comparison failed: ${error.message}`);
	}
}

function renderTraceBlock(parent, span, title, value) {
	const section = document.createElement("section");
	section.className = "trace-detail-block";
	const header = document.createElement("div");
	header.className = "trace-block-header";
	const heading = document.createElement("h3");
	heading.textContent = title;
	const actions = document.createElement("div");
	actions.className = "trace-block-actions";
	const blockKey = `${span.id}:${title}`;
	const mode = state.traceBlockModes[blockKey] || "text";
	["text", "json"].forEach((candidate) => {
		const button = document.createElement("button");
		button.type = "button";
		button.className = "trace-block-mode";
		button.textContent = candidate.toUpperCase();
		button.classList.toggle("active", mode === candidate);
		button.addEventListener("click", () => {
			state.traceBlockModes[blockKey] = candidate;
			renderTrace();
		});
		actions.append(button);
	});
	const copy = document.createElement("button");
	copy.type = "button";
	copy.className = "trace-block-copy";
	copy.textContent = "Copy";
	copy.addEventListener("click", () => copyTraceBlock(title, value));
	actions.append(copy);
	const data = document.createElement("pre");
	data.className = "code-block";
	data.textContent = mode === "json" ? formatJSON(value) : formatTraceText(value);
	header.append(heading, actions);
	section.append(header, data);
	parent.append(section);
}

function formatTraceText(value) {
	const parsed = parseTracePayload(value);
	if (typeof parsed === "string") return parsed;
	if (Array.isArray(parsed) && parsed.every((item) => item?.content)) {
		return parsed.map((item) => `${item.role || "message"}: ${item.content}`).join("\n\n");
	}
	if (parsed?.content && Object.keys(parsed).every((key) => ["role", "content", "attachments", "created_at"].includes(key))) {
		return parsed.content;
	}
	return formatJSON(parsed);
}

async function copyTraceBlock(title, value) {
	try {
		await navigator.clipboard.writeText(formatJSON(value));
		toast(`${title} copied`);
	} catch (error) {
		toast(`Copy failed: ${error.message}`);
	}
}

function parseTracePayload(value) {
	if (typeof value !== "string") return value;
	try {
		return JSON.parse(value);
	} catch {
		return value;
	}
}

async function copySelectedTraceSpan() {
	const span = (state.trace.spans || []).find((item) => item.id === state.traceSelection);
	if (!span) return;
	try {
		await navigator.clipboard.writeText(JSON.stringify(span, null, 2));
		toast("Span JSON copied");
	} catch (error) {
		toast(`Copy failed: ${error.message}`);
	}
}

function formatTraceDuration(span) {
	if (typeof span.duration_ms === "number" && span.duration_ms >= 0) return `${span.duration_ms}ms`;
	const started = Date.parse(span.started_at);
	const completed = Date.parse(span.completed_at);
	if (Number.isFinite(started) && Number.isFinite(completed) && completed >= started) return `${completed - started}ms`;
	return span.status === "running" ? "in progress" : "—";
}

function escapeHTML(value) {
	const node = document.createElement("span");
	node.textContent = value;
	return node.innerHTML;
}

async function refreshPlan() {
  if (!state.currentRunId) {
    selectPlan(null);
    return;
  }
  const plan = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/plan`, {
    method: "GET",
  });
  selectPlan(plan);
}

async function refreshApproval() {
	  const previousApprovalID = state.pendingApproval?.id || "";
	  if (!state.currentRunId) {
	    state.pendingApproval = null;
	    renderApproval();
	    return;
	  }
	  try {
	    const data = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/approvals/pending`, {
	      method: "GET",
	    });
	    state.pendingApproval = data.approval || null;
	  } catch (error) {
	    if (error.status === 404) {
	      state.pendingApproval = null;
	    } else {
	      state.pendingApproval = null;
	      toast(`Approval load failed: ${error.message}`);
	    }
	  }
	  renderApproval();
	  if (state.pendingApproval && state.pendingApproval.id !== previousApprovalID) {
	    selectInspector("approvalPanel");
	    toast("Shell approval is waiting for your decision");
	  }
}

async function resolveShellApproval(decision) {
	  const approval = state.pendingApproval;
	  if (!state.currentRunId || !approval) {
	    toast("No shell approval is pending");
	    return;
	  }
	  await withBusy(async () => {
	    const data = await requestJSON(
	      `/v1/runs/${encodeURIComponent(state.currentRunId)}/approvals/${encodeURIComponent(approval.id)}`,
	      { method: "POST", body: JSON.stringify({ decision }) },
	    );
	    selectRun(data.run);
	    selectPlan(data.plan);
	    state.pendingApproval = null;
	    await Promise.all([refreshMessages(), refreshEvents(), refreshModelLogs(), refreshApproval(), loadRunHistory()]);
	    toast(`Shell approval ${decision}d`);
	  });
}

async function setPlanMode(enabled) {
  if (!state.currentRunId) {
    state.plan = { mode: enabled ? "planning" : "off", steps: [] };
    renderPlan();
    return;
  }
  await withBusy(async () => {
    const data = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/plan-mode`, {
      method: "POST",
      body: JSON.stringify({ enabled }),
    });
    selectRun(data.run);
    selectPlan(data.plan);
    await Promise.all([refreshEvents(), loadRunHistory()]);
  });
}

async function executePlan() {
  if (!state.currentRunId) {
    toast("No run selected");
    return;
  }
  await withBusy(async () => {
    const data = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/plan/execute`, {
      method: "POST",
      body: JSON.stringify({}),
    });
    selectRun(data.run);
    selectPlan(data.plan);
    await Promise.all([refreshEvents(), loadRunHistory()]);
    toast("Plan execution enabled");
  });
}

async function resetPlan() {
  if (!state.currentRunId) {
    state.plan = { mode: "off", steps: [] };
    renderPlan();
    return;
  }
  await withBusy(async () => {
    const data = await requestJSON(`/v1/runs/${encodeURIComponent(state.currentRunId)}/plan/reset`, {
      method: "POST",
      body: JSON.stringify({}),
    });
    selectRun(data.run);
    selectPlan(data.plan);
    await Promise.all([refreshEvents(), loadRunHistory()]);
    toast("Plan reset");
  });
}

async function submitTurn(streaming) {
  const message = el.messageInput.value.trim();
  const attachments = state.attachments.map(toRequestAttachment);
  if (!message && attachments.length === 0) {
    toast("Message or image is required");
    return;
  }

  await withBusy(async () => {
    el.messageInput.value = "";
    clearSelectedAttachments();
    const optimistic = appendOptimisticUserMessage(message, attachments);
    try {
      if (!state.currentRunId) {
        if (streaming) {
          const data = await requestJSON("/v1/runs", {
            method: "POST",
            body: JSON.stringify({ plan_mode: el.planModeToggle.checked }),
          });
          selectRun(data.run);
          selectPlan(data.plan);
          await streamTurn(message, attachments);
        } else {
          const data = await requestJSON("/v1/runs", {
            method: "POST",
            body: JSON.stringify({ message, attachments, plan_mode: el.planModeToggle.checked }),
          });
          selectRun(data.run);
          selectPlan(data.plan);
          renderMessages(data.messages || []);
          await Promise.all([refreshEvents(), refreshModelLogs(), refreshApproval(), loadRunHistory()]);
          toast("Run created and turn completed");
        }
        return;
      }

      if (streaming) {
        await streamTurn(message, attachments);
      } else {
        const data = await requestJSON(
          `/v1/runs/${encodeURIComponent(state.currentRunId)}/turns`,
          {
            method: "POST",
            body: JSON.stringify({ message, attachments }),
          },
        );
        selectRun(data.run);
        selectPlan(data.plan);
        renderMessages(data.messages || []);
        await Promise.all([refreshEvents(), refreshModelLogs(), refreshApproval(), loadRunHistory()]);
      }
    } catch (error) {
      markOptimisticMessage(optimistic, "failed");
      el.messageInput.value = message;
      state.attachments = attachments.map((attachment) => ({
        ...attachment,
        preview_url: attachmentURL(attachment),
      }));
      renderAttachmentPreview();
      throw error;
    }
  });
}

async function streamTurn(message, attachments) {
  const response = await fetch(
    apiURL(`/v1/runs/${encodeURIComponent(state.currentRunId)}/turns/stream`),
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ message, attachments }),
    },
  );
  if (!response.ok || !response.body) {
    throw new Error(`${response.status} ${response.statusText}`);
  }

  state.events = [];
  state.streamingAssistant = null;
  renderEvents();
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
    buffer = consumeSSEBuffer(buffer);
    if (done) {
      break;
    }
  }

  await Promise.all([refreshRunMetadata(), refreshMessages(), refreshEvents(), refreshModelLogs(), refreshPlan(), refreshApproval(), loadRunHistory()]);
}

function consumeSSEBuffer(buffer) {
  const chunks = buffer.split("\n\n");
  const remaining = chunks.pop() || "";
  chunks.forEach((chunk) => {
    const lines = chunk.split("\n");
    const eventLine = lines.find((line) => line.startsWith("event:"));
    const dataLine = lines.find((line) => line.startsWith("data:"));
    if (!dataLine) {
      return;
    }
    try {
      const event = JSON.parse(dataLine.slice(5).trim());
      if (eventLine && !event.type) {
        event.type = eventLine.slice(6).trim();
      }
      handleStreamEvent(event);
      renderEvents();
    } catch (error) {
      toast(`Invalid SSE payload: ${error.message}`);
    }
  });
  return remaining;
}

function handleStreamEvent(event) {
  state.events.push(event);
  if (event.type === "model_started") {
    state.streamingAssistant = null;
    return;
  }
  if (event.type !== "model_output_delta" || typeof event.data?.delta !== "string") {
    return;
  }
  if (!state.streamingAssistant) {
    state.streamingAssistant = appendMessageCard({
      role: "assistant",
      content: "",
      created_at: event.created_at,
      pending_state: "streaming",
    });
  }
  const body = state.streamingAssistant.querySelector(".card-body");
  body.textContent += event.data.delta;
  el.messageTimeline.scrollTop = el.messageTimeline.scrollHeight;
}

function selectRun(run) {
	if (state.currentRunId !== run.id) {
		stopTraceEventStream();
		state.traceExpanded = new Set();
		state.traceExpansionInitialized = false;
		state.traceSelection = null;
		state.traceEventSelection = null;
		state.traceEvents = { available: false, events: [], nextSequence: 0, diagnostics: null, status: "idle" };
		state.traceEventSequences = new Set();
	}
	state.currentRunId = run.id;
  el.currentRunId.textContent = run.id;
  el.runIdInput.value = run.id;
  el.runStatus.textContent = run.status || "-";
  el.modelProvider.textContent = run.model_provider || "-";
  el.tokenUsage.textContent = formatUsageSummary(run.token_usage);
  el.planStatus.textContent = planLabelFromRun(run);
  el.updatedAt.textContent = formatDate(run.updated_at);
  upsertRun(run);
  renderHistory();
	updateRunRoute();
}

function selectPlan(plan) {
  state.plan = plan || { mode: "off", steps: [] };
  el.planModeToggle.checked = state.plan.mode === "planning";
  el.planStatus.textContent = planLabel(state.plan);
  renderPlan();
}

function upsertRun(run) {
  state.runs = [run, ...state.runs.filter((item) => item.id !== run.id)].slice(0, 40);
}

function renderHistory() {
  el.historyCount.textContent = state.runs.length;
  el.historyList.textContent = "";
  if (state.runs.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty-history";
    empty.textContent = "No persisted runs yet.";
    el.historyList.append(empty);
    return;
  }

  state.runs.forEach((run) => {
    const button = document.createElement("button");
    button.className = "history-item";
    if (run.id === state.currentRunId) {
      button.classList.add("active");
    }
    button.type = "button";
    button.textContent = run.plan_mode && run.plan_mode !== "off"
      ? `${run.id} (${run.plan_mode})`
      : run.id;
    button.title = `${run.status || "unknown"} / ${formatDate(run.updated_at)}`;
    button.addEventListener("click", () => loadRun(run.id));
    el.historyList.append(button);
  });
}

function renderPlan() {
  const plan = state.plan || { mode: "off", steps: [] };
  const steps = plan.steps || [];
  el.planView.classList.remove("empty-state");
  el.planView.textContent = "";
	const awaitingApproval = Boolean(state.pendingApproval);
	el.executePlanButton.disabled = state.busy || awaitingApproval || !state.currentRunId || steps.length === 0 || plan.mode === "executing";
	el.resetPlanButton.disabled = state.busy || awaitingApproval || !state.currentRunId || (plan.mode === "off" && steps.length === 0);

  if (plan.mode === "off" && steps.length === 0) {
    el.planView.classList.add("empty-state");
    el.planView.textContent = "Enable Plan Mode to create a plan.";
    return;
  }

  const summary = document.createElement("div");
  summary.className = "plan-summary";
  const label = document.createElement("span");
  label.className = "plan-mode-pill";
  label.textContent = planLabel(plan);
  const count = document.createElement("strong");
  count.textContent = `${completedPlanSteps(plan)}/${steps.length} completed`;
  summary.append(label, count);
  el.planView.append(summary);

  if (steps.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty-state";
    empty.textContent = plan.mode === "planning" ? "Send a message to generate a plan." : "No plan steps yet.";
    el.planView.append(empty);
    return;
  }

  const list = document.createElement("ol");
  list.className = "plan-steps";
  steps.forEach((step) => {
    const item = document.createElement("li");
    item.className = `plan-step ${step.status === "completed" ? "completed" : ""}`;
    const index = document.createElement("span");
    index.className = "plan-step-index";
    index.textContent = step.status === "completed" ? "✓" : String(step.index);
    const text = document.createElement("span");
    text.textContent = step.text;
    item.append(index, text);
    list.append(item);
  });
  el.planView.append(list);
}

function renderApproval() {
	  const approval = state.pendingApproval;
	  const pending = Boolean(approval);
	  el.approvalInspectorTab.classList.toggle("has-pending", pending);
	  el.approvalInspectorTab.setAttribute("aria-label", pending ? "Approval, pending decision" : "Approval");
	  el.approvalPendingBadge.hidden = !pending;
	  el.approvalView.textContent = "";
	  if (!approval) {
	    el.approvalView.classList.add("empty-state");
	    el.approvalView.textContent = "No shell approval is pending.";
	    setApprovalActionsDisabled(true);
	    return;
	  }
	  el.approvalView.classList.remove("empty-state");
	  const overview = document.createElement("div");
	  overview.className = "approval-overview";
	  const stateLabel = document.createElement("span");
	  stateLabel.className = "approval-state-label";
	  stateLabel.textContent = "Pending your decision";
	  const intro = document.createElement("p");
	  intro.textContent = "This command requires explicit approval before it can run.";
	  overview.append(stateLabel, intro);

	  const facts = document.createElement("dl");
	  facts.className = "approval-facts";
	  appendApprovalFact(facts, "Workspace", approval.workspace || "-");
	  appendApprovalFact(facts, "Tool", approval.tool_call?.name || "shell");
	  el.approvalView.append(overview, facts);

	  const risk = document.createElement("section");
	  risk.className = "approval-risk";
	  const riskTitle = document.createElement("h3");
	  riskTitle.textContent = "Why approval is required";
	  const reasons = document.createElement("ul");
	  reasons.className = "approval-reasons";
	  (approval.risk_reasons || []).forEach((reason) => {
	    const item = document.createElement("li");
	    item.textContent = reason;
	    reasons.append(item);
	  });
	  if (reasons.childElementCount === 0) {
		  const item = document.createElement("li");
		  item.textContent = "This command requires manual review.";
		  reasons.append(item);
	  }
	  risk.append(riskTitle, reasons);

	  const command = document.createElement("section");
	  command.className = "approval-command";
	  const commandTitle = document.createElement("h3");
	  commandTitle.textContent = "Command to run";
	  const script = document.createElement("pre");
	  script.className = "approval-script";
	  script.textContent = shellCommandFromApproval(approval);
	  command.append(commandTitle, script);
	  el.approvalView.append(risk, command);
	  setApprovalActionsDisabled(state.busy);
}

function appendApprovalFact(parent, label, value) {
	const term = document.createElement("dt");
	term.textContent = label;
	const detail = document.createElement("dd");
	detail.textContent = value;
	detail.title = value;
	parent.append(term, detail);
}

function shellCommandFromApproval(approval) {
	  const argumentsValue = approval.tool_call?.arguments;
	  if (argumentsValue && typeof argumentsValue === "object") {
	    return argumentsValue.command || "";
	  }
	  try {
	    return JSON.parse(argumentsValue || "{}").command || "";
	  } catch (_) {
	    return "(stored shell arguments could not be decoded)";
	  }
}

function setApprovalActionsDisabled(value) {
	  el.approveShellButton.disabled = value;
	  el.denyShellButton.disabled = value;
	  el.cancelShellButton.disabled = value;
}

function planLabelFromRun(run) {
  if (!run?.plan_mode || run.plan_mode === "off") {
    return "off";
  }
  const total = run.plan_steps || 0;
  const completed = run.plan_completed || 0;
  return total > 0 ? `${run.plan_mode} ${completed}/${total}` : run.plan_mode;
}

function planLabel(plan) {
  if (!plan?.mode) {
    return "off";
  }
  if (plan.mode === "off" && (plan.steps || []).length > 0) {
    return "complete";
  }
  return plan.mode;
}

function completedPlanSteps(plan) {
  return (plan.steps || []).filter((step) => step.status === "completed").length;
}

function renderMessages(messages) {
  el.messageTimeline.classList.remove("empty-state");
  el.messageTimeline.textContent = "";
  if (!messages.length) {
    el.messageTimeline.classList.add("empty-state");
    el.messageTimeline.textContent = "No messages in this run.";
    return;
  }

  messages.forEach((message) => {
    if (message.tool_calls?.length) {
      if (!isToolCallOnlyMessage(message)) {
        appendMessageCard(message);
      }
      message.tool_calls.forEach((toolCall) => appendToolCallCard(toolCall));
      return;
    }
    appendMessageCard(message);
  });
  el.messageTimeline.scrollTop = el.messageTimeline.scrollHeight;
}

function isToolCallOnlyMessage(message) {
  return (
    message.role === "assistant" &&
    !(message.content || "").trim() &&
    (message.attachments || []).length === 0
  );
}

function appendOptimisticUserMessage(content, attachments) {
  el.messageTimeline.classList.remove("empty-state");
  if (el.messageTimeline.textContent === "No messages in this run." || el.messageTimeline.textContent === "Select or create a run to inspect messages.") {
    el.messageTimeline.textContent = "";
  }
  const message = {
    role: "user",
    content,
    attachments,
    created_at: new Date().toISOString(),
    pending_state: "pending",
  };
  return appendMessageCard(message);
}

function markOptimisticMessage(card, stateName) {
  if (!card) {
    return;
  }
  card.classList.remove("pending");
  card.classList.add(stateName);
  const pill = card.querySelector(".status-pill");
  if (pill) {
    pill.textContent = stateName;
  }
}

function appendMessageCard(message) {
  const card = document.createElement("article");
  card.className = `message-card ${message.role || "assistant"}`;
  if (message.pending_state) {
    card.classList.add(message.pending_state);
  }

  const header = document.createElement("div");
  header.className = "card-header";
  const title = document.createElement("span");
  title.textContent = message.name
    ? `${message.role}: ${message.name}`
    : message.role || "message";
  header.append(title);

  const meta = document.createElement("div");
  meta.className = "card-meta";

  const time = document.createElement("time");
  time.className = "message-time";
  time.textContent = formatDate(message.created_at);
  meta.append(time);

  if (message.usage) {
    const usage = document.createElement("span");
    usage.className = "message-usage";
    usage.textContent = formatCompactUsage(message.usage);
    usage.title = formatUsageSummary(message.usage);
    meta.append(usage);
  }

  if (message.pending_state || message.role === "tool") {
    const pill = document.createElement("span");
    pill.className = "status-pill";
    pill.textContent = message.pending_state || "completed";
    meta.append(pill);
  }
  header.append(meta);

  const body = document.createElement("div");
  body.className = "card-body";
  body.textContent = message.content || ((message.attachments || []).length > 0 ? "Image attachment" : "(empty)");

  card.append(header, body);
  appendMessageAttachments(card, message.attachments || []);
  el.messageTimeline.append(card);
  el.messageTimeline.scrollTop = el.messageTimeline.scrollHeight;
  return card;
}

function appendMessageAttachments(card, attachments) {
  const images = attachments.filter((attachment) => attachment.type === "image" && attachment.data);
  if (images.length === 0) {
    return;
  }
  const grid = document.createElement("div");
  grid.className = "message-images";
  images.forEach((attachment) => {
    const image = document.createElement("img");
    image.src = attachment.preview_url || attachmentURL(attachment);
    image.alt = attachment.name || "Attached image";
    grid.append(image);
  });
  card.append(grid);
}

function appendToolCallCard(toolCall) {
  const card = document.createElement("article");
  card.className = "tool-card";

  const header = document.createElement("div");
  header.className = "card-header";
  const title = document.createElement("span");
  title.textContent = `Using Tool: ${toolCall.name || "unknown"}`;
  header.append(title);

  const body = document.createElement("div");
  body.className = "card-body";

  const label = document.createElement("span");
  label.className = "muted-label";
  label.textContent = "Arguments";

  const code = document.createElement("pre");
  code.className = "code-block";
  code.textContent = formatJSON(toolCall.arguments);

  body.append(label, code);
  card.append(header, body);
  el.messageTimeline.append(card);
}

function renderEvents() {
  el.eventStream.classList.remove("empty-state");
  el.eventStream.textContent = "";
  if (!state.events.length) {
    el.eventStream.classList.add("empty-state");
    el.eventStream.textContent = "No events yet.";
    return;
  }

  state.events.forEach((event) => {
    const card = document.createElement("article");
    card.className = "event-card";

    const type = document.createElement("span");
    type.className = "event-type";
    type.textContent = event.type || "event";

    const time = document.createElement("time");
    time.textContent = formatDate(event.created_at);

    const body = document.createElement("div");
    body.className = "code-block";
    body.textContent = event.message || formatJSON(event);

    card.append(type, time);
    if (event.usage) {
      card.append(renderUsageDetails(event.usage));
    }
    card.append(body);
    el.eventStream.append(card);
  });
  el.eventStream.scrollTop = el.eventStream.scrollHeight;
}

function renderModelLogs() {
  el.modelLogsView.classList.remove("empty-state");
  el.modelLogsView.textContent = "";
  if (!state.modelLogs.length) {
    el.modelLogsView.classList.add("empty-state");
    el.modelLogsView.textContent = "No model I/O logs yet.";
    return;
  }

  state.modelLogs.forEach((log) => {
    const card = document.createElement("article");
    card.className = "model-log-card";

    const header = document.createElement("div");
    header.className = "model-log-header";
    const title = document.createElement("strong");
    title.textContent = `Call ${log.index || "?"}`;
    const time = document.createElement("time");
    time.textContent = formatDate(log.started_at);
    header.append(title, time);
    card.append(header);

    if (log.response?.usage) {
      card.append(renderUsageDetails(log.response.usage));
    }
    if (log.error) {
      const error = document.createElement("div");
      error.className = "model-log-error";
      error.textContent = log.error;
      card.append(error);
    }

    card.append(renderModelLogBlock("Request", log.request));
    if (log.response && (log.response.message || log.response.usage)) {
      card.append(renderModelLogBlock("Response", log.response));
    }
    el.modelLogsView.append(card);
  });
  el.modelLogsView.scrollTop = el.modelLogsView.scrollHeight;
}

function renderModelLogBlock(labelText, value) {
  const block = document.createElement("div");
  block.className = "model-log-block";
  const label = document.createElement("span");
  label.className = "muted-label";
  label.textContent = labelText;
  const code = document.createElement("pre");
  code.className = "code-block";
  code.textContent = formatJSON(value);
  block.append(label, code);
  return block;
}

function renderUsageDetails(usage) {
  const grid = document.createElement("div");
  grid.className = "usage-grid";
  [
    ["Prompt", usage.prompt_tokens],
    ["Completion", usage.completion_tokens],
    ["Total", usage.total_tokens],
    ["Cached", usage.cached_tokens],
    ["Reasoning", usage.reasoning_tokens],
  ].forEach(([label, value]) => {
    if (value === undefined || value === null) {
      return;
    }
    const item = document.createElement("span");
    item.className = "usage-chip";
    item.textContent = `${label}: ${value}`;
    grid.append(item);
  });
  return grid;
}

async function handleImageSelection(event) {
  const files = Array.from(event.target.files || []);
  for (const file of files) {
    if (!file.type.startsWith("image/")) {
      toast(`${file.name} is not an image`);
      continue;
    }
    if (file.size > maxImageBytes) {
      toast(`${file.name} exceeds 5MB`);
      continue;
    }
    try {
      const dataURL = await readFileAsDataURL(file);
      const commaIndex = dataURL.indexOf(",");
      if (commaIndex === -1) {
        toast(`${file.name} could not be read`);
        continue;
      }
      state.attachments.push({
        type: "image",
        media_type: file.type,
        data: dataURL.slice(commaIndex + 1),
        name: file.name,
        preview_url: dataURL,
      });
    } catch (error) {
      toast(error.message);
    }
  }
  el.imageInput.value = "";
  renderAttachmentPreview();
}

function readFileAsDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(new Error(`${file.name} could not be read`));
    reader.readAsDataURL(file);
  });
}

function renderAttachmentPreview() {
  el.attachmentPreview.textContent = "";
  if (state.attachments.length === 0) {
    el.attachmentPreview.hidden = true;
    return;
  }
  el.attachmentPreview.hidden = false;
  state.attachments.forEach((attachment, index) => {
    const item = document.createElement("div");
    item.className = "attachment-item";

    const image = document.createElement("img");
    image.src = attachment.preview_url || attachmentURL(attachment);
    image.alt = attachment.name || "Selected image";

    const remove = document.createElement("button");
    remove.type = "button";
    remove.title = "Remove image";
    remove.textContent = "Remove";
    remove.addEventListener("click", () => {
      state.attachments.splice(index, 1);
      renderAttachmentPreview();
    });

    item.append(image, remove);
    el.attachmentPreview.append(item);
  });
}

function clearSelectedAttachments() {
  state.attachments = [];
  renderAttachmentPreview();
}

function toRequestAttachment(attachment) {
  return {
    type: attachment.type,
    media_type: attachment.media_type,
    data: attachment.data,
    name: attachment.name,
  };
}

function attachmentURL(attachment) {
  return `data:${attachment.media_type};base64,${attachment.data}`;
}

function formatJSON(value) {
  if (value === undefined || value === null || value === "") {
    return "{}";
  }
  if (typeof value === "string") {
    try {
      return JSON.stringify(JSON.parse(value), null, 2);
    } catch {
      return value;
    }
  }
  return JSON.stringify(value, null, 2);
}

function formatDate(value) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

function formatUsageSummary(usage) {
  if (!usage) {
    return "-";
  }
  const parts = [];
  if (usage.total_tokens !== undefined && usage.total_tokens !== null) {
    parts.push(`total ${usage.total_tokens}`);
  }
  if (usage.prompt_tokens !== undefined && usage.prompt_tokens !== null) {
    parts.push(`prompt ${usage.prompt_tokens}`);
  }
  if (usage.cached_tokens !== undefined && usage.cached_tokens !== null) {
    parts.push(`cached ${usage.cached_tokens}`);
  }
  return parts.length ? parts.join(" / ") : "-";
}

function formatCompactUsage(usage) {
  if (!usage) {
    return "-";
  }
  const parts = [];
  if (usage.total_tokens !== undefined && usage.total_tokens !== null) {
    parts.push(`T ${usage.total_tokens}`);
  }
  if (usage.prompt_tokens !== undefined && usage.prompt_tokens !== null) {
    parts.push(`P ${usage.prompt_tokens}`);
  }
  if (usage.cached_tokens !== undefined && usage.cached_tokens !== null) {
    parts.push(`C ${usage.cached_tokens}`);
  }
  if (usage.completion_tokens !== undefined && usage.completion_tokens !== null) {
    parts.push(`O ${usage.completion_tokens}`);
  }
  return parts.length ? parts.join(" / ") : "-";
}

async function copyCurrentRunId() {
  if (!state.currentRunId) {
    toast("No run selected");
    return;
  }
  await navigator.clipboard.writeText(state.currentRunId);
  toast("Run ID copied");
}

async function withBusy(work) {
  if (state.busy) {
    return;
  }
  state.busy = true;
  setBusy(true);
  try {
    await work();
  } catch (error) {
    toast(error.message);
  } finally {
    state.busy = false;
    setBusy(false);
  }
}

function setBusy(value) {
	  const awaitingApproval = Boolean(state.pendingApproval);
  [
    el.newRunButton,
    el.loadRunButton,
    el.refreshRunButton,
    el.refreshMessagesButton,
    el.refreshEventsButton,
    el.refreshModelLogsButton,
    el.executePlanButton,
    el.resetPlanButton,
    el.sendButton,
    el.streamButton,
  ].forEach((button) => {
	    button.disabled = value || awaitingApproval;
  });
	el.planModeToggle.disabled = value || awaitingApproval;
	  setApprovalActionsDisabled(value || !awaitingApproval);
  renderPlan();
}

function toast(message) {
  el.toast.textContent = message;
  el.toast.classList.add("visible");
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => {
    el.toast.classList.remove("visible");
  }, 2600);
}

init();
