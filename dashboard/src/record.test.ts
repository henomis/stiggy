import { describe, expect, it } from "vitest";

import type { Entry } from "./api";
import { argsText } from "./Record";
import { entryKey } from "./Story";

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
