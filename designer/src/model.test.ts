import { describe, expect, it } from "vitest";

import { BLOCK, BLOCKS, newBlock } from "./blocks";
import type { Block, Workflow } from "./project";
import { clean, identifier, newProject, onlyRef, refToken, splitRefs } from "./project";
import { available, describe as describeRef, upstream } from "./refs";

const block = (id: string, type: Block["type"], params: Record<string, unknown> = {}): Block => ({
  id,
  type,
  name: id.toUpperCase(),
  position: { x: 0, y: 0 },
  params,
});

const wf: Workflow = {
  id: "w",
  name: "W",
  nodes: [
    block("s", "start", { inputs: [{ name: "topic", type: "text" }] }),
    block("plan", "agent", { output: [{ name: "angles", type: "list" }] }),
    block("loop", "loop"),
    block("write", "agent"),
    block("side", "agent"),
  ],
  edges: [
    { id: "1", from: { node: "s", port: "out" }, to: { node: "plan" } },
    { id: "2", from: { node: "plan", port: "out" }, to: { node: "loop" } },
    { id: "3", from: { node: "loop", port: "out" }, to: { node: "write" } },
    // A loop back must not make write its own upstream.
    { id: "4", from: { node: "write", port: "out" }, to: { node: "plan" } },
  ],
};

describe("references", () => {
  it("round-trip through tokens", () => {
    expect(refToken({ root: "start", path: "topic" })).toBe("{{ref start topic}}");
    expect(onlyRef(" {{ref n1 text}} ")).toEqual({ root: "n1", path: "text" });
    expect(onlyRef("x {{ref n1 text}}")).toBeNull();
    expect(splitRefs("a {{ref start topic}} b {{ref n2}}")).toEqual(["a ", { root: "start", path: "topic" }, " b ", { root: "n2", path: "" }]);
  });

  it("read as names", () => {
    expect(describeRef(wf, { root: "plan", path: "angles" }).text).toBe("PLAN › angles");
    expect(describeRef(wf, { root: "write", path: "text" }).text).toBe("WRITE › answer");
    expect(describeRef(wf, { root: "gone", path: "" }).missing).toBe(true);
  });
});

describe("available values", () => {
  it("are the inputs and the blocks before, not after or beside", () => {
    expect(upstream(wf, "loop").map((n) => n.id).sort()).toEqual(["loop", "plan", "s", "write"].filter((x) => x !== "loop").sort());
    const labels = available(wf, wf.nodes[2]).map((v) => `${v.source}.${v.label}`);
    expect(labels).toContain("Current item.item");
    expect(labels).toContain("Start.topic");
    expect(labels).toContain("PLAN.angles");
    expect(labels).not.toContain("SIDE.answer");
  });
});

describe("blocks", () => {
  it("have ports and defaults", () => {
    const p = newProject();
    for (const d of BLOCKS) {
      const b = newBlock(d.type, p, { x: 0, y: 0 }, []);
      expect(b.name).toBe(d.label);
      expect(Array.isArray(d.ports(b))).toBe(true);
      expect(typeof d.summary(b)).toBe("string");
    }
    expect(BLOCK.condition.ports(block("c", "condition")).map((x) => x.id)).toEqual(["true", "false"]);
    expect(BLOCK.router.ports(block("r", "router", { routes: [{ port: "a" }] })).map((x) => x.id)).toEqual(["a", "otherwise"]);
  });

  it("get unique names", () => {
    expect(newBlock("agent", newProject(), { x: 0, y: 0 }, ["AI Agent", "AI Agent 2"]).name).toBe("AI Agent 3");
  });
});

describe("helpers", () => {
  it("clean drops what was not set", () => {
    expect(clean({ a: "", b: 0, c: false, d: [], e: "x", f: undefined })).toEqual({ b: 0, e: "x" });
  });

  it("identifier makes field names", () => {
    expect(identifier("Fact Check!")).toBe("fact_check");
  });
});
