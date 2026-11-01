// Pure helpers shared by the chat page and its unit tests (vitest).
import type { AttachmentOut, ChatSession, Project, UsageInfo } from "@/lib/api";

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


/** isDayChange reports whether the two timestamps fall on different calendar
 * days (used to render day separators in long-lived conversations). */
export function isDayChange(prevIso: string | undefined, iso: string | undefined): boolean {
  if (!iso) return false;
  if (!prevIso) return true; // first message always opens a day header
  const a = new Date(prevIso);
  const b = new Date(iso);
  if (isNaN(a.getTime()) || isNaN(b.getTime())) return false;
  return a.getFullYear() !== b.getFullYear() || a.getMonth() !== b.getMonth() || a.getDate() !== b.getDate();
}

/** dayLabel formats a timestamp as "10月30日 星期四" style day header. */
export function dayLabel(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleDateString("zh-CN", { month: "long", day: "numeric", weekday: "long" });
}

export type SessionGroupKey = "pinned" | "today" | "yesterday" | "week" | "earlier" | "archived";

export interface SessionGroup {
  key: SessionGroupKey;
  label: string;
  sessions: ChatSession[];
}

export interface ProjectGroup {
  /** key is "proj:<id>", an ad-hoc dir root, "default" or "archived". */
  key: string;
  label: string;
  /** Full workspace root for tooltips; undefined for the default project. */
  path?: string;
  /** Registered project the group belongs to, if any. */
  projectId?: string;
  sessions: ChatSession[];
}

const SESSION_GROUP_LABELS: Record<SessionGroupKey, string> = {
  pinned: "置顶",
  today: "今天",
  yesterday: "昨天",
  week: "7 天内",
  earlier: "更早",
  archived: "已归档",
};

const SESSION_GROUP_ORDER: SessionGroupKey[] = ["pinned", "today", "yesterday", "week", "earlier", "archived"];

const byRecency = (a: ChatSession, b: ChatSession): number =>
  (b.updatedAt ?? "").localeCompare(a.updatedAt ?? "");

/** groupSessionsByTime buckets sidebar sessions: pinned first (age-agnostic),
 * then by local calendar day of updatedAt, archived always last. Sessions with
 * missing or invalid timestamps land in "earlier"; empty buckets are omitted. */
export function groupSessionsByTime(sessions: ChatSession[], now: Date): SessionGroup[] {
  const startOf = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const today = startOf(now);
  const buckets = new Map<SessionGroupKey, ChatSession[]>();
  for (const s of sessions) {
    let key: SessionGroupKey = "earlier";
    if (s.archived) {
      key = "archived";
    } else if (s.pinned) {
      key = "pinned";
    } else {
      const t = s.updatedAt ? new Date(s.updatedAt).getTime() : NaN;
      if (!isNaN(t)) {
        const days = Math.round((today - startOf(new Date(t))) / 86400000);
        if (days <= 0) key = "today";
        else if (days === 1) key = "yesterday";
        else if (days < 7) key = "week";
      }
    }
    const list = buckets.get(key);
    if (list) list.push(s);
    else buckets.set(key, [s]);
  }
  return SESSION_GROUP_ORDER
    .filter((key) => buckets.has(key))
    .map((key) => ({ key, label: SESSION_GROUP_LABELS[key], sessions: (buckets.get(key) ?? []).sort(byRecency) }));
}

/** groupSessionsByProject buckets sidebar sessions for the "项目" grouping.
 * Registered projects come first: sessions bound via projectId — and legacy
 * sessions whose explicit workspace equals a project root — join that
 * project's group. Remaining explicit workspaces outside the global one keep
 * their ad-hoc folder groups; auto task dirs under the global workspace and
 * workspace-less sessions form the default project. Archived sessions trail
 * in their own group; groups sort by their newest member. */
export function groupSessionsByProject(sessions: ChatSession[], projects: Project[], globalWorkspace: string): ProjectGroup[] {
  const norm = (p: string) => p.replace(/\\/g, "/").replace(/\/+$/, "").toLowerCase();
  const root = norm(globalWorkspace);
  const underGlobal = (dir: string) => {
    const d = norm(dir);
    return root !== "" && (d === root || d.startsWith(root + "/"));
  };
  const baseName = (dir: string) => {
    const parts = norm(dir).split("/").filter(Boolean);
    return parts[parts.length - 1] ?? dir;
  };

  const groups = new Map<string, ProjectGroup>();
  const push = (key: string, label: string, path: string | undefined, s: ChatSession, projectId?: string) => {
    const g = groups.get(key);
    if (g) g.sessions.push(s);
    else groups.set(key, { key, label, path, projectId, sessions: [s] });
  };
  const byId = new Map(projects.map((p) => [p.id, p]));
  const byRoot = new Map(projects.map((p) => [norm(p.rootDir), p]));
  for (const s of sessions) {
    if (s.archived) {
      push("archived", SESSION_GROUP_LABELS.archived, undefined, s);
      continue;
    }
    const registered = (s.projectId ? byId.get(s.projectId) : undefined)
      ?? (s.workspaceDir ? byRoot.get(norm(s.workspaceDir)) : undefined);
    if (registered) {
      push(`proj:${registered.id}`, registered.name, registered.rootDir, s, registered.id);
      continue;
    }
    const dir = s.workspaceDir || "";
    if (!dir || underGlobal(dir)) {
      push("default", "默认项目", undefined, s);
    } else {
      // Dedupe case-insensitively (Windows paths) but keep the first-seen
      // original spelling as the display key/path.
      const existing = [...groups.values()].find((g) => norm(g.key) === norm(dir));
      if (existing) existing.sessions.push(s);
      else push(dir, baseName(dir), dir, s);
    }
  }
  const newest = (g: ProjectGroup) => g.sessions.reduce((max, s) => (s.updatedAt ?? "") > max ? s.updatedAt ?? "" : max, "");
  return [...groups.values()]
    .sort((a, b) => (a.key === "archived" ? 1 : b.key === "archived" ? -1 : newest(b).localeCompare(newest(a))))
    .map((g) => ({ ...g, sessions: g.sessions.sort(byRecency) }));
}
