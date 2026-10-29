import { describe, expect, it } from "vitest";
import {
  computeEditFlags, computeRunningTools, computeToolDurations, fmtTime, mdToSpeech,
  type UIMessage,
} from "./chatUtils";

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
    expect(fmtTime(today.toISOString())).toMatch(/^09:05$/);
    const old = new Date(2020, 0, 15, 9, 5);
    expect(fmtTime(old.toISOString())).toMatch(/^01-15 09:05$/);
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
