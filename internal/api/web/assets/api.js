export async function request(path, options = {}) {
  const response = await fetch(path, options);
  const text = await response.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { message: text };
    }
  }
  if (!response.ok) {
    const message = data?.message || data?.error || text || response.statusText;
    const error = new Error(message);
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return data;
}

export function postJSON(path, body) {
  return request(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {})
  });
}

export function streamRun(path, input, onEvent) {
  return fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ input })
  }).then(async response => {
    if (!response.ok || !response.body) {
      const message = await response.text();
      throw new Error(message || response.statusText);
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const frames = buffer.split("\n\n");
      buffer = frames.pop() || "";
      for (const frame of frames) {
        const event = parseSSEFrame(frame);
        if (event) onEvent(event);
      }
    }
    if (buffer.trim()) {
      const event = parseSSEFrame(buffer);
      if (event) onEvent(event);
    }
  });
}

function parseSSEFrame(frame) {
  const lines = frame.split(/\r?\n/);
  const eventType = (lines.find(line => line.startsWith("event:")) || "").slice(6).trim();
  const dataLine = lines.find(line => line.startsWith("data:"));
  if (!dataLine) return null;
  try {
    const payload = JSON.parse(dataLine.slice(5).trim());
    payload.type = payload.type || eventType;
    return payload;
  } catch {
    return { type: eventType || "text", content: dataLine.slice(5).trim() };
  }
}

export function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

export function formatList(values) {
  if (!values || values.length === 0) return "-";
  return values.join(", ");
}
