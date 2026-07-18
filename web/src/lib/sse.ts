import { ApiError, formatApiDetail } from "./api";
import type { SseEvent, SseEventMap, SseEventName } from "./types";

export interface StreamSseOptions {
  signal?: AbortSignal;
  onEvent: (event: SseEvent) => void;
}

const knownEvents = new Set<SseEventName>([
  "text_delta",
  "tool_start",
  "rate_wait",
  "tool_result",
  "citation",
  "usage",
  "done",
  "error",
]);

function parseFrame(frame: string): SseEvent | null {
  if (!frame.trim() || frame.trimStart().startsWith(":")) return null;
  let eventName = "message";
  const dataLines: string[] = [];
  for (const line of frame.split("\n")) {
    if (line.startsWith("event:")) eventName = line.slice(6).trim();
    if (line.startsWith("data:")) dataLines.push(line.slice(5).trimStart());
  }
  if (!knownEvents.has(eventName as SseEventName) || dataLines.length === 0) return null;
  const event = eventName as SseEventName;
  const data = JSON.parse(dataLines.join("\n")) as SseEventMap[typeof event];
  return { event, data } as SseEvent;
}

async function streamError(response: Response): Promise<ApiError> {
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  const detail = payload && typeof payload === "object" && "detail" in payload
    ? (payload as { detail: unknown }).detail
    : payload;
  return new ApiError(response.status, detail ?? formatApiDetail(null, response.status), payload);
}

export async function streamSse(
  path: string,
  body: Record<string, unknown>,
  { signal, onEvent }: StreamSseOptions,
): Promise<void> {
  const response = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: {
      Accept: "text/event-stream",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
    signal,
  });
  if (!response.ok) throw await streamError(response);
  if (!response.body) throw new ApiError(502, "Sunucudan akış alınamadı.");

  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8");
  let buffer = "";

  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });
    buffer = buffer.replace(/\r\n/g, "\n");
    let boundary = buffer.indexOf("\n\n");
    while (boundary >= 0) {
      const frame = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const parsed = parseFrame(frame);
      if (parsed) onEvent(parsed);
      boundary = buffer.indexOf("\n\n");
    }
    if (done) break;
  }

  const finalFrame = parseFrame(buffer);
  if (finalFrame) onEvent(finalFrame);
}
