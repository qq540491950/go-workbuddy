import { describe, expect, it } from "vitest";
import {
  computeEditFlags, computeRunningTools, computeToolDurations, dayLabel, fmtTime, isDayChange,
  mdToSpeech, type UIMessage,
  groupSessionsByTime,
  groupSessionsByProject,
} from "./chatUtils";
import type { ChatSession } from "@/lib/api";

const msg = (over: Partial<UIMessage>): UIMessage => ({
  id: over.id ?? Math.random().toString(),
  kind: "assistant",
  author: "bot",
  text: "",
  ...over,
});

describe("computeToolDurations", () => {
  it("pairs results by tool name and restarts the clock at confirmation", () => {
    const t0 = "2026-10-30T01:00:00Z";
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "write", timestamp: t0 }),
      msg({ id: "c2", kind: "tool_call", toolName: "read", timestamp: t0 }),
      // Parallel round: result B arrives before result A — name pairing must
      // not end A's clock early.
      msg({ id: "r2", kind: "tool_result", toolName: "read", timestamp: "2026-10-30T01:00:10Z" }),
      msg({ id: "r1", kind: "tool_result", toolName: "write", timestamp: "2026-10-30T01:00:20Z" }),
    ];
    const d = computeToolDurations(msgs);
    expect(d.get("r2")).toBe(10_000);
    expect(d.get("r1")).toBe(20_000);
  });

  it("re-arms the duration clock at a HITL confirmation card", () => {
    const msgs: UIMessage[] = [
      msg({ id: "c1", kind: "tool_call", toolName: "write", timestamp: "2026-10-30T01:00:00Z" }),
      msg({ id: "cf", kind: "confirm", toolName: "write", timestamp: "2026-10-30T01:05:00Z" }),
      msg({ id: "r1", kind: "tool_result", toolName: "write", timestamp: "2026-10-30T01:05:03Z" }),
    ];
    // The 5 minutes of human thinking must not count into the tool duration.
    expect(computeToolDurations(msgs).get("r1")).toBe(3_000);
  });
});

describe("computeRunningTools", () => {
  it("keeps parallel calls spinning until their own result arrives", () => {
    const msgs: UIMessage[] = [
      msg({ id: "a", kind: "tool_call", toolName: "A" }),
      msg({ id: "b", kind: "tool_call", toolName: "B" }),
      msg({ id: "rb", kind: "tool_result", toolName: "B" }),
    ];
    const running = computeRunningTools(msgs);
    expect(running.has("a")).toBe(true); // A still running
    expect(running.has("b")).toBe(false); // B finished
  });
});

describe("computeEditFlags", () => {
  it("marks only the last user message as editable", () => {
    const msgs: UIMessage[] = [
      msg({ id: "u1", kind: "user", text: "one" }),
      msg({ id: "a1", kind: "assistant", text: "two" }),
      msg({ id: "u2", kind: "user", text: "three" }),
    ];
    const flags = computeEditFlags(msgs);
    expect(flags.get("u2")).toBe(true);
    expect(flags.get("u1")).toBeUndefined();
  });
});

describe("fmtTime", () => {
  it("shows only the time for today and adds the date otherwise", () => {
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 9, 5);
    // Locale data varies across Node ICU builds — anchor on the stable parts.
    expect(fmtTime(today.toISOString())).toContain("09:05");
    const old = new Date(2020, 0, 15, 9, 5);
    expect(fmtTime(old.toISOString())).toContain("01-15");
    expect(fmtTime(old.toISOString())).toContain("09:05");
    expect(fmtTime("garbage")).toBe("");
  });
});

describe("mdToSpeech", () => {
  it("strips markdown structures for speech synthesis", () => {
    const out = mdToSpeech("# 标题\n\n这是 **加粗** 与 `代码`\n\n```js\nconst x = 1;\n```");
    expect(out).not.toContain("#");
    expect(out).not.toContain("**");
    expect(out).not.toContain("const x");
    expect(out).toContain("标题");
    expect(out).toContain("这是 加粗 与 代码");
  });
});

describe("isDayChange / dayLabel", () => {
  it("marks calendar-day boundaries and labels them in Chinese", () => {
    const a = new Date(2026, 9, 29, 23, 0).toISOString();
    const b = new Date(2026, 9, 30, 0, 30).toISOString();
    expect(isDayChange(a, b)).toBe(true);
    expect(isDayChange(b, b)).toBe(false);
    expect(isDayChange(undefined, b)).toBe(true);
    // Full-icu renders 「10月30日 星期四」; small-icu builds degrade the
    // format — only assert a non-empty localized label.
    expect(dayLabel(b).length).toBeGreaterThan(0);
    expect(dayLabel("bad")).toBe("");
  });
});

describe('groupSessionsByTime', () => {
  const NOW = new Date('2026-11-01T12:00:00');
  const sess = (over: Partial<ChatSession>): ChatSession => ({
    id: over.id ?? Math.random().toString(),
    title: over.title ?? '会话',
    targetType: '',
    targetId: '',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-11-01T08:00:00',
    ...over,
  });

  it('buckets pinned/today/yesterday/week/earlier/archived in fixed order, newest first within groups', () => {
    const groups = groupSessionsByTime(
      [
        sess({ id: 'old', updatedAt: '2026-09-01T10:00:00' }),
        sess({ id: 'edge7', updatedAt: '2026-10-25T12:00:00' }),
        sess({ id: 'arch', updatedAt: '2026-11-01T09:00:00', archived: true }),
        sess({ id: 'yest', updatedAt: '2026-10-31T22:00:00' }),
        sess({ id: 'week', updatedAt: '2026-10-30T08:00:00' }),
        sess({ id: 'pin', updatedAt: '2026-09-15T10:00:00', pinned: true }),
        sess({ id: 'today2', updatedAt: '2026-11-01T11:00:00' }),
        sess({ id: 'today1', updatedAt: '2026-11-01T07:00:00' }),
      ],
      NOW,
    );
    expect(groups.map((g) => [g.key, g.label, g.sessions.map((s) => s.id)])).toEqual([
      ['pinned', '置顶', ['pin']],
      ['today', '今天', ['today2', 'today1']],
      ['yesterday', '昨天', ['yest']],
      ['week', '7 天内', ['week']],
      ['earlier', '更早', ['edge7', 'old']],
      ['archived', '已归档', ['arch']],
    ]);
  });

  it('omits empty groups and falls back to earlier for missing or invalid timestamps', () => {
    const groups = groupSessionsByTime(
      [
        sess({ id: 'a', updatedAt: '' }),
        sess({ id: 'b', updatedAt: 'not-a-date' }),
        sess({ id: 'c', updatedAt: '2026-06-01T00:00:00' }),
      ],
      NOW,
    );
    expect(groups.map((g) => g.key)).toEqual(['earlier']);
  });
});

describe('groupSessionsByProject', () => {
  const sess = (over: Partial<ChatSession>): ChatSession => ({
    id: over.id ?? Math.random().toString(),
    title: over.title ?? '会话',
    targetType: '',
    targetId: '',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-11-01T08:00:00',
    ...over,
  });
  const WS = 'C:/ws';

  it('groups explicit workspace roots by folder name and auto task dirs into the default project', () => {
    const groups = groupSessionsByProject(
      [
        sess({ id: 'auto', workspaceDir: 'C:/ws/tasks/sess1', updatedAt: '2026-11-01T09:00:00' }),
        sess({ id: 'legacy' }),
        sess({ id: 'alpha-new', workspaceDir: 'D:/proj/alpha', updatedAt: '2026-11-01T10:00:00' }),
        sess({ id: 'alpha-old', workspaceDir: 'D:/proj/alpha', updatedAt: '2026-10-20T10:00:00' }),
        sess({ id: 'caseback', workspaceDir: 'c:\\WS\\tasks\\x', updatedAt: '2026-11-01T07:00:00' }),
        sess({ id: 'gone', workspaceDir: 'E:/archived-proj', archived: true }),
      ],
      WS,
    );
    expect(groups.map((g) => [g.key, g.label, g.sessions.map((s) => s.id)])).toEqual([
      ['D:/proj/alpha', 'alpha', ['alpha-new', 'alpha-old']],
      ['default', '默认项目', ['auto', 'legacy', 'caseback']],
      ['archived', '已归档', ['gone']],
    ]);
    expect(groups[0].path).toBe('D:/proj/alpha');
    expect(groups[1].path).toBeUndefined();
  });

  it('sorts project groups by newest member', () => {
    const groups = groupSessionsByProject(
      [
        sess({ id: 'a1', workspaceDir: 'D:/a', updatedAt: '2026-10-01T00:00:00' }),
        sess({ id: 'b1', workspaceDir: 'D:/b', updatedAt: '2026-10-31T00:00:00' }),
      ],
      WS,
    );
    expect(groups.map((g) => g.label)).toEqual(['b', 'a']);
  });
});
