// Pure helpers shared by the chat page and its unit tests (vitest).
import type { AttachmentOut, UsageInfo } from "@/lib/api";

export type UIMessage = {
  id: string;
  kind: "user" | "assistant" | "tool_call" | "tool_result" | "confirm" | "error";
  author: string;
  text: string;
  toolName?: string;
  toolArgs?: { [k: string]: any } | null;
  toolResp?: { [k: string]: any } | null;
  callId?: string;
  hint?: string;
  confirmStatus?: "pending" | "approved" | "rejected";
  timestamp?: string;
  durationMs?: number;
  streaming?: boolean;
  usage?: UsageInfo | null;
  attachments?: AttachmentOut[] | null;
};

/** mdToSpeech converts markdown to a speakable plain string. */
export function mdToSpeech(md: string): string {
  return md
    .replace(/```[\s\S]*?```/g, "（代码块已省略）")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/[*_~>|]/g, "")
    .replace(/\s+\n/g, "\n")
    .trim();
}

/** fmtTime renders an ISO timestamp for hover display: HH:MM for today,
 * MM-DD HH:MM otherwise (long-lived sessions span days). */
export function fmtTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const time = d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  const today = new Date();
  if (d.getFullYear() === today.getFullYear() && d.getMonth() === today.getMonth() && d.getDate() === today.getDate()) {
    return time;
  }
  const day = `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  return `${day} ${time}`;
}

/**
 * computeToolDurations pairs tool_result with its call to derive durations.
 * A HITL confirmation parks the run between the call and the result, so the
 * confirmed call's clock restarts at the confirmation card: otherwise the
 * displayed time is how long the human took to click, not how long the tool ran.
 */
export function computeToolDurations(messages: UIMessage[]): Map<string, number> {
  const map = new Map<string, number>();
  const pending: { id: string; toolName: string; ts: number }[] = [];
  for (const m of messages) {
    const ts = m.timestamp ? Date.parse(m.timestamp) : Date.now();
    if (m.kind === "confirm") {
      // The paused tool is the most recent call of the same name.
      if (m.timestamp) {
        const idx = [...pending].reverse().findIndex((p) => p.toolName === m.toolName);
        if (idx >= 0) pending[pending.length - 1 - idx].ts = ts;
      }
      continue;
    }
    if (m.kind !== "tool_call" && m.kind !== "tool_result") continue;
    if (m.kind === "tool_call") {
      pending.push({ id: m.id, toolName: m.toolName ?? "", ts });
    } else {
      const idx = [...pending].reverse().findIndex((p) => p.toolName === m.toolName);
      if (idx >= 0) {
        const call = pending.splice(pending.length - 1 - idx, 1)[0];
        map.set(m.id, Math.max(0, ts - call.ts));
      }
    }
  }
  return map;
}

/** computeRunningTools marks tool_call messages that have no matching result yet.
 * Pairing is by tool name (same as computeToolDurations): parallel/团队 tool
 * rounds emit call A, call B, result B, result A — a FIFO queue would end A's
 * spinner early and leave B spinning forever. */
export function computeRunningTools(messages: UIMessage[]): Set<string> {
  const running = new Set<string>();
  const pending: { id: string; toolName: string }[] = [];
  for (const m of messages) {
    if (m.kind === "tool_call") {
      running.add(m.id);
      pending.push({ id: m.id, toolName: m.toolName ?? "" });
    } else if (m.kind === "tool_result") {
      const idx = [...pending].reverse().findIndex((p) => p.toolName === (m.toolName ?? ""));
      if (idx >= 0) {
        const call = pending.splice(pending.length - 1 - idx, 1)[0];
        running.delete(call.id);
      }
    }
  }
  return running;
}

/**
 * computeEditFlags marks the user messages that may be edited: only the last
 * one, because editing replaces that turn and everything after it. Doing this
 * per row was O(n^2) on every streamed token.
 */
export function computeEditFlags(messages: UIMessage[]): Map<string, boolean> {
  const flags = new Map<string, boolean>();
  let lastUserID: string | null = null;
  for (const m of messages) {
    if (m.kind === "user") lastUserID = m.id;
  }
  if (lastUserID) flags.set(lastUserID, true);
  return flags;
}

