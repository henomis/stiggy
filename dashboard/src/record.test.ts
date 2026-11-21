import { describe, expect, it } from "vitest";

import type { Entry, Field, RecordCall, RecordRun, RecordToolCall, Usage } from "./api";
import { href, parseRoute } from "./hooks";
import { argsText } from "./Record";
import { entryKey, tokenFact } from "./Story";
import {
  answerPlan,
  decodeRec,
  defaultOpen,
  encodeRec,
  ledgerParts,
  matchChildren,
  siblings,
  detailsLabel,
  plainText,
  sameInputs,
  spanAttempt,
  stepPrompt,
  unrecordedSteps,
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

describe("detailsLabel", () => {
  const u = (x: Partial<Usage>): Usage => ({ tokens_in: 1, tokens_out: 1, cost_usd: 0, ...x });
  it("says why to open it, by the outcome", () => {
    expect(detailsLabel("failed", u({ llm_calls: 5 }), false)).toBe("Details: how it failed");
    expect(detailsLabel("retry", undefined, false)).toBe("Details: how it failed");
    expect(detailsLabel("waiting", u({ tool_calls: 3 }), false)).toBe("Details: how it got to this question");
    expect(detailsLabel("failed", undefined, true)).toBe("Hide details");
    expect(detailsLabel("done", u({ llm_calls: 3 }), true)).toBe("Hide details");
  });
  it("counts the calls of a long loop", () => {
    expect(detailsLabel("done", u({ llm_calls: 18, tool_calls: 22, tool_errors: 2 }), false)).toBe(
      "Details: 18 model calls, 22 tool calls (2 failed)",
    );
    expect(detailsLabel("done", u({ llm_calls: 1, tool_calls: 1 }), false)).toBe("Details: 1 model call, 1 tool call");
    expect(detailsLabel("cached", u({ llm_calls: 2 }), false)).toBe("Details: 2 model calls");
  });
  it("names the system prompt of a single call", () => {
    expect(detailsLabel("done", u({ llm_calls: 1 }), false)).toBe("Details: system prompt");
    expect(detailsLabel("done", undefined, false)).toBe("Details: system prompt");
    expect(detailsLabel("done", u({ llm_calls: 1 }), false, "remote")).toBe("Details");
  });
});

describe("plainText", () => {
  const text = (key: string, t: string, kind: Field["kind"] = "text"): Field => ({ key, kind, text: t });
  it("reads a plain answer", () => {
    expect(plainText([text("text", "hello")])).toBe("hello");
    expect(plainText([text("text", "")])).toBe("");
  });
  it("leaves anything else as fields", () => {
    expect(plainText(undefined)).toBeUndefined();
    expect(plainText([])).toBeUndefined();
    expect(plainText([text("summary", "hello")])).toBeUndefined();
    expect(plainText([text("text", "3", "number")])).toBeUndefined();
    expect(plainText([text("text", "a"), text("score", "3", "number")])).toBeUndefined();
  });
});

describe("stepPrompt", () => {
  it("is the last user message of the first call", () => {
    const r = run({
      calls: [
        {
          usage,
          message_offset: 0,
          messages: [
            { role: "user", text: "earlier" },
            { role: "assistant", text: "reply" },
            { role: "user", text: "now" },
          ],
        },
        { usage, message_offset: 3, messages: [{ role: "user", text: "later" }] },
      ],
    });
    expect(stepPrompt(r)).toEqual({ msg: { role: "user", text: "now" }, index: 2 });
    expect(stepPrompt(run())).toBeUndefined();
  });
});

describe("sameInputs", () => {
  const att = (attempt: number, text: string, x: Partial<Entry> = {}): Entry => ({
    kind: "step", node: "w", key: "w", generation: 1, attempt, recorded: true, time: "2026-01-01T00:00:00Z",
    input: { text, size: text.length }, ...x,
  });
  it("marks a retry that put the same prompt", () => {
    const a1 = att(1, "p", { outcome: "retry" });
    const a2 = att(2, "p", { outcome: "retry" });
    const a3 = att(3, "q");
    const m = sameInputs([a1, a2, a3]);
    expect(m.get(a2)).toBe(1);
    expect(m.has(a1)).toBe(false);
    expect(m.has(a3)).toBe(false);
  });
  it("keeps visits and steps apart", () => {
    const a = att(1, "p");
    const b = att(1, "p", { generation: 2 });
    const c = att(2, "p", { node: "x", key: "x" });
    expect(sameInputs([a, b, c]).size).toBe(0);
  });
  it("needs both texts", () => {
    const a = att(1, "", { input: { text: "", size: 0, missing: "not_kept" } });
    const b = att(2, "", { input: { text: "", size: 0, missing: "not_kept" } });
    expect(sameInputs([a, b]).size).toBe(0);
  });
});

describe("unrecordedSteps", () => {
  const step = (node: string, x: Partial<Entry>): Entry => ({ kind: "step", node, time: "2026-01-01T00:00:00Z", outcome: "done", ...x });
  it("counts the agent and remote steps without a record, once each", () => {
    const story = [
      step("a", { role: "agent" }),
      step("a", { role: "agent", attempt: 2 }),
      step("r", { role: "remote" }),
      step("t", { role: "activity" }),
      step("k", { role: "agent", recorded: true }),
      step("c", { role: "agent", outcome: "cached" }),
    ];
    expect(unrecordedSteps(story)).toBe(2);
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
  it("leaves it to the story entry that shows it as text", () => {
    expect(answerPlan("text", r).kind).toBe("none");
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

describe("tokenFact", () => {
  it("splits input and output", () => {
    expect(tokenFact({ tokens_in: 458, tokens_out: 292, cost_usd: 0 })).toBe("458 in, 292 out tokens");
  });
  it("adds cached and reasoning shares when present", () => {
    expect(tokenFact({ tokens_in: 12_300, tokens_out: 900, cache_read: 8_000, reasoning: 400, cost_usd: 0 })).toBe(
      "12.3k in, 900 out, 8k cached, 400 reasoning tokens",
    );
  });
});
