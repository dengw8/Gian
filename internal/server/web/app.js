const maxImageBytes = 5 * 1024 * 1024;

const state = {
  apiBase: window.location.origin,
  currentRunId: "",
  runs: [],
  events: [],
  attachments: [],
  busy: false,
};

const el = {
  attachmentPreview: document.querySelector("#attachmentPreview"),
  copyRunButton: document.querySelector("#copyRunButton"),
  currentRunId: document.querySelector("#currentRunId"),
  eventStream: document.querySelector("#eventStream"),
  healthStatus: document.querySelector("#healthStatus"),
  historyCount: document.querySelector("#historyCount"),
  historyList: document.querySelector("#historyList"),
  imageButton: document.querySelector("#imageButton"),
  imageInput: document.querySelector("#imageInput"),
  loadRunButton: document.querySelector("#loadRunButton"),
  messageInput: document.querySelector("#messageInput"),
  messageTimeline: document.querySelector("#messageTimeline"),
  modelProvider: document.querySelector("#modelProvider"),
  newRunButton: document.querySelector("#newRunButton"),
  refreshEventsButton: document.querySelector("#refreshEventsButton"),
  refreshMessagesButton: document.querySelector("#refreshMessagesButton"),
  refreshRunButton: document.querySelector("#refreshRunButton"),
  runIdInput: document.querySelector("#runIdInput"),
  runStatus: document.querySelector("#runStatus"),
  sendButton: document.querySelector("#sendButton"),
  streamButton: document.querySelector("#streamButton"),
  toast: document.querySelector("#toast"),
  updatedAt: document.querySelector("#updatedAt"),
};

function init() {
  renderHistory();
  bindEvents();
  checkHealth();
  loadRunHistory();
}

function bindEvents() {
  el.newRunButton.addEventListener("click", createEmptyRun);
  el.loadRunButton.addEventListener("click", loadRunFromInput);
  el.copyRunButton.addEventListener("click", copyCurrentRunId);
  el.refreshRunButton.addEventListener("click", refreshRunMetadata);
  el.refreshMessagesButton.addEventListener("click", refreshMessages);
  el.refreshEventsButton.addEventListener("click", refreshEvents);
  el.imageButton.addEventListener("click", () => el.imageInput.click());
  el.imageInput.addEventListener("change", handleImageSelection);
  el.sendButton.addEventListener("click", () => submitTurn(false));
  el.streamButton.addEventListener("click", () => submitTurn(true));

  el.messageInput.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submitTurn(false);
    }
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
    throw new Error(data?.error || `${response.status} ${response.statusText}`);
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
      body: JSON.stringify({}),
    });
    selectRun(data.run);
    renderMessages(data.messages || []);
    await Promise.all([refreshEvents(), loadRunHistory()]);
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
    await Promise.all([refreshMessages(), refreshEvents(), loadRunHistory()]);
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
        const data = await requestJSON("/v1/runs", {
          method: "POST",
          body: JSON.stringify({ message, attachments }),
        });
        selectRun(data.run);
        renderMessages(data.messages || []);
        await Promise.all([refreshEvents(), loadRunHistory()]);
        toast("Run created and turn completed");
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
        renderMessages(data.messages || []);
        await Promise.all([refreshEvents(), loadRunHistory()]);
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

  await Promise.all([refreshRunMetadata(), refreshMessages(), refreshEvents(), loadRunHistory()]);
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
      state.events.push(event);
      renderEvents();
    } catch (error) {
      toast(`Invalid SSE payload: ${error.message}`);
    }
  });
  return remaining;
}

function selectRun(run) {
  state.currentRunId = run.id;
  el.currentRunId.textContent = run.id;
  el.runIdInput.value = run.id;
  el.runStatus.textContent = run.status || "-";
  el.modelProvider.textContent = run.model_provider || "-";
  el.updatedAt.textContent = formatDate(run.updated_at);
  upsertRun(run);
  renderHistory();
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
    button.textContent = run.id;
    button.title = `${run.status || "unknown"} / ${formatDate(run.updated_at)}`;
    button.addEventListener("click", () => loadRun(run.id));
    el.historyList.append(button);
  });
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
      appendMessageCard(message);
      message.tool_calls.forEach((toolCall) => appendToolCallCard(toolCall));
      return;
    }
    appendMessageCard(message);
  });
  el.messageTimeline.scrollTop = el.messageTimeline.scrollHeight;
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

    card.append(type, time, body);
    el.eventStream.append(card);
  });
  el.eventStream.scrollTop = el.eventStream.scrollHeight;
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
  [
    el.newRunButton,
    el.loadRunButton,
    el.refreshRunButton,
    el.refreshMessagesButton,
    el.refreshEventsButton,
    el.sendButton,
    el.streamButton,
  ].forEach((button) => {
    button.disabled = value;
  });
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
