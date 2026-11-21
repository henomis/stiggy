import { describe, expect, it } from "vitest";

import { ago, clock, duration, money, preview, shortId, tokens } from "./format";
import { layoutTrail } from "./trail";

describe("clock", () => {
  const now = Date.parse("2026-11-20T12:00:00");
  it("shows the time alone for today", () => {
    expect(clock("2026-11-20T09:05:07", now)).toMatch(/^\d{2}\D\d{2}\D\d{2}/);
  });
  it("adds the date for another day", () => {
    expect(clock("2026-11-18T09:05:07", now)).toMatch(/18.*, \d{2}\D\d{2}\D\d{2}|Nov.*18/);
  });
});

describe("format", () => {
  it("abbreviates tokens", () => {
    expect(tokens(0)).toBe("0");
    expect(tokens(950)).toBe("950");
    expect(tokens(1500)).toBe("1.5k");
    expect(tokens(12_340)).toBe("12.3k");
    expect(tokens(2_000_000)).toBe("2M");
  });

  it("formats money", () => {
    expect(money(0)).toBe("$0");
    expect(money(0.004)).toBe("<$0.01");
    expect(money(3.456)).toBe("$3.46");
    expect(money(1234.5)).toBe("$1,235");
  });

  it("formats durations", () => {
    expect(duration(420)).toBe("420ms");
    expect(duration(1_500)).toBe("1.5s");
    expect(duration(12_000)).toBe("12s");
    expect(duration(250_000)).toBe("4m 10s");
    expect(duration(7_500_000)).toBe("2h 05m");
    expect(duration(100_000_000)).toBe("1d 3h");
  });

  it("says how long ago", () => {
    const now = Date.parse("2026-11-20T12:00:00Z");
    expect(ago("2026-11-20T11:59:50Z", now)).toBe("just now");
    expect(ago("2026-11-20T11:56:00Z", now)).toBe("4m ago");
    expect(ago("2026-11-19T10:00:00Z", now)).toMatch(/^yesterday, \d{2}\D\d{2}\D\d{2}/);
    expect(ago("2026-11-10T10:00:00Z", now)).toMatch(/2026.*\d{2}\D\d{2}\D\d{2}/);
  });

  it("previews fields in one line", () => {
    expect(preview([{ key: "topic", text: "ants" }, { key: "n", text: "3" }])).toBe("topic: ants, n: 3");
    expect(preview([{ key: "value", text: "just\n text" }])).toBe("just text");
    expect([...preview([{ key: "t", text: "é".repeat(300) }])].length).toBe(140);
  });

  it("shortens long ids only", () => {
    expect(shortId("abc")).toBe("abc");
    expect(shortId("01JD7Q8ZK3M4N5P6Q7R8S9T0V1")).toBe("…Q7R8S9T0V1");
  });
});

describe("trail layout", () => {
  it("orders nodes by depth from the start and keeps back edges", () => {
    const l = layoutTrail({
      flow: "f",
      start: "a",
      nodes: [
        { id: "a", type: "task" },
        { id: "b", type: "task" },
        { id: "c", type: "task" },
      ],
      edges: [
        { from: "a", to: "b", kind: "next" },
        { from: "b", to: "c", kind: "next" },
        { from: "c", to: "a", kind: "route" },
      ],
    });
    expect(l.nodes.map((n) => n.col)).toEqual([0, 1, 2]);
    expect(l.edges.find((e) => e.from === "c")?.back).toBe(true);
  });

  it("stacks branches in rows", () => {
    const l = layoutTrail({
      flow: "f",
      start: "fan",
      nodes: [
        { id: "fan", type: "fanout" },
        { id: "x", type: "task" },
        { id: "y", type: "task" },
        { id: "join", type: "join" },
      ],
      edges: [
        { from: "fan", to: "x", kind: "branch" },
        { from: "fan", to: "y", kind: "branch" },
        { from: "x", to: "join", kind: "next" },
        { from: "y", to: "join", kind: "next" },
      ],
    });
    const byId = Object.fromEntries(l.nodes.map((n) => [n.id, n]));
    expect(byId.x.col).toBe(1);
    expect(byId.y.col).toBe(1);
    expect(byId.x.row).not.toBe(byId.y.row);
    expect(byId.join.col).toBe(2);
  });
});
