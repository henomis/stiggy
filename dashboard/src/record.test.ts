import { describe, expect, it } from "vitest";

import type { Entry, RecordCall, RecordRun, RecordToolCall } from "./api";
import { href, parseRoute } from "./hooks";
import { argsText } from "./Record";
import { entryKey } from "./Story";
import {
  answerPlan,
  decodeRec,
  defaultOpen,
  encodeRec,
  ledgerParts,
  matchChildren,
  siblings,
  spanAttempt,
  toggleLabel,
} from "./recordNav";

describe("argsText", () => {
  it("lists an object's fields", () => {
    expect(argsText('{"query":"ants","n":3,"deep":{"a":1}}')).toBe('query: ants\nn: 3\ndeep: {"a":1}');
  });
  it("names empty arguments", () => {
    expect(argsText("{}")).toBe("(no arguments)");
  });
  it("keeps anything else as is", () => {
    expect(argsText("not json")).toBe("not json");
    expect(argsText("[1,2]")).toBe("[1,2]");
  });
});

describe("entryKey", () => {
  const step = (node: string, attempt: number): Entry => ({
    kind: "step", node, key: node, generation: 1, attempt, outcome: "done", time: "2026-01-01T00:00:00Z", end: `2026-01-01T00:00:0${attempt}Z`,
  });
  const human: Entry = { kind: "human", node: "write", time: "2026-01-01T00:00:05Z", text: "ok" };

  it("follows a step when the list is filtered", () => {
    const all = [human, step("research", 1), step("write", 1)];
    const steps = all.filter((e) => e.kind === "step");
    expect(entryKey(steps[1], 1)).toBe(entryKey(all[2], 2));
  });
  it("tells attempts apart", () => {
    expect(entryKey(step("write", 1), 0)).not.toBe(entryKey(step("write", 2), 0));
    expect(entryKey(step("write", 1), 0)).not.toBe(entryKey({ ...step("write", 1), generation: 2 }, 0));
  });
});

const usage = { input_tokens: 1, output_tokens: 1 };
const call = (x: Partial<RecordCall> = {}): RecordCall => ({ usage, message_offset: 0, ...x });
const run = (x: Partial<RecordRun> = {}): RecordRun => ({ agent: "a", usage, ...x });

describe("toggleLabel", () => {
  it("names the record by the outcome", () => {
    expect(toggleLabel("done", false)).toBe("How it got this answer");
    expect(toggleLabel("cached", false)).toBe("How it got this answer");
    expect(toggleLabel("failed", false)).toBe("How it failed");
    expect(toggleLabel("retry", false)).toBe("How it failed");
    expect(toggleLabel("waiting", false)).toBe("How it got to this question");
    expect(toggleLabel("failed", true)).toBe("Hide");
  });
});

describe("rec links", () => {
  const at = { node: "a.b", key: "a.b/3.x", generation: 2, attempt: 3 };
  it("round-trip, dots and slashes included", () => {
    expect(decodeRec(encodeRec(at))).toEqual(at);
    expect(encodeRec({ node: "write", key: "write", generation: 1, attempt: 2 })).toBe("write.write.1.2");
  });
  it("refuse what is not an attempt", () => {
    expect(decodeRec("a.b.c")).toBeUndefined();
    expect(decodeRec("a.b.x.1")).toBeUndefined();
    expect(decodeRec("a.b.0.1")).toBeUndefined();
    expect(decodeRec("%E0.b.1.1")).toBeUndefined();
    expect(decodeRec(undefined)).toBeUndefined();
  });
  it("go through the route", () => {
    const h = href.execution("news room", "ex.1", at);
    expect(parseRoute(h)).toEqual({ page: "execution", ns: "news room", id: "ex.1", rec: at });
    expect(parseRoute("#/ns/n/x/e")).toEqual({ page: "execution", ns: "n", id: "e" });
    expect(parseRoute("#/ns/n/x/e?rec=bad")).toEqual({ page: "execution", ns: "n", id: "e" });
    expect(parseRoute("#/ns/n?flow=f")).toEqual({ page: "namespace", ns: "n", flow: "f", status: undefined });
  });
});

describe("matchChildren", () => {
  it("matches delegates to their tool calls in order", () => {
    const t1: RecordToolCall = { tool: "ask_researcher" };
    const t2: RecordToolCall = { tool: "search" };
    const t3: RecordToolCall = { tool: "ask_researcher" };
    const calls = [call({ tool_calls: [t1, t2] }), call({ tool_calls: [t3] })];
    const c1 = run({ agent: "researcher", via: "delegate", tool: "ask_researcher" });
    const c2 = run({ agent: "researcher", via: "delegate", tool: "ask_researcher" });
    const inside = run({ agent: "helper", via: "agent" });
    const lost = run({ agent: "x", via: "delegate", tool: "ask_x" });
    const m = matchChildren(calls, [c1, inside, c2, lost]);
    expect(m.byCall.get(t1)).toBe(c1);
    expect(m.byCall.get(t3)).toBe(c2);
    expect(m.byCall.has(t2)).toBe(false);
    expect(m.rest).toEqual([inside, lost]);
  });
});

describe("defaultOpen", () => {
  it("opens failures and the last call", () => {
    expect(defaultOpen(call(), false)).toBe(false);
    expect(defaultOpen(call(), true)).toBe(true);
    expect(defaultOpen(call({ error: "boom" }), false)).toBe(true);
    expect(defaultOpen(call({ tool_calls: [{ tool: "t" }, { tool: "u", error: "no" }] }), false)).toBe(true);
  });
});

describe("answerPlan", () => {
  const r = run({ output: "the answer", calls: [call({ response: "the answer" })] });
  it("folds the raw answer when the fields show it", () => {
    expect(answerPlan("fields", r)).toMatchObject({ kind: "raw", text: "the answer" });
  });
  it("shows the last answer open when there are no fields", () => {
    expect(answerPlan(undefined, run({ calls: [call({ response: "first" }), call({ error: "x" }), call({ response: "late", response_truncated: true })] }))).toEqual({
      kind: "open",
      text: "late",
      cut: true,
    });
  });
  it("leaves it to the tool card that shows it", () => {
    expect(answerPlan("tool", r).kind).toBe("none");
  });
  it("shows nothing without an answer", () => {
    expect(answerPlan(undefined, run({ calls: [call({ error: "x" })] })).kind).toBe("none");
  });
});

describe("ledgerParts", () => {
  it("keeps what is new, and moves reasoning to the call that answered", () => {
    const calls = [
      call({ messages: [{ role: "user", text: "q" }], response: "let me look", tool_calls: [{ tool: "search" }] }),
      call({
        message_offset: 1,
        messages: [
          { role: "assistant", text: "let me look", reasoning: "think" },
          { role: "tool", text: "result" },
          { role: "user", text: "more" },
        ],
      }),
    ];
    const p = ledgerParts(calls);
    expect(p[0]).toEqual({ msgs: [], reasoning: "think" });
    expect(p[1].msgs).toEqual([{ role: "user", text: "more" }]);
  });
});

describe("attempt siblings", () => {
  const step = (attempt: number, time: string, x: Partial<Entry> = {}): Entry => ({
    kind: "step", node: "write", key: "write", generation: 1, attempt, recorded: true, outcome: "retry", time, ...x,
  });
  const story: Entry[] = [
    step(1, "2026-01-01T00:00:00Z"),
    { kind: "human", node: "write", time: "2026-01-01T00:00:01Z" },
    step(2, "2026-01-01T00:00:02Z"),
    step(1, "2026-01-01T00:00:03Z", { node: "edit", key: "edit" }),
    step(3, "2026-01-01T00:00:04Z", { recorded: false }),
    step(1, "2026-01-01T00:01:00Z", { generation: 2 }),
  ];
  it("lists the recorded attempts of a step in story order", () => {
    const s = siblings(story, { node: "write", key: "write", generation: 1, attempt: 1 });
    expect(s.map((a) => [a.generation, a.attempt])).toEqual([[1, 1], [1, 2], [2, 1]]);
  });
  it("finds the attempt of a timeline bar by its start", () => {
    const a = spanAttempt(story, { node: "write", key: "write", attempt: 1, start: "2026-01-01T00:00:59Z", outcome: "done" });
    expect(a).toEqual({ node: "write", key: "write", generation: 2, attempt: 1 });
    expect(spanAttempt(story, { node: "write", key: "write", attempt: 3, start: "2026-01-01T00:00:04Z", outcome: "done" })).toBeUndefined();
  });
});
