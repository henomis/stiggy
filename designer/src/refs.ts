// What a block can refer to: the workflow's inputs, the answers of the blocks
// before it, and, inside a loop, the current item.

import type { Block, Field, Ref, Workflow } from "./project";

export interface Value {
  ref: Ref;
  /** Where it comes from: "Start", a block name, "Current item". */
  source: string;
  /** What it is: an input name, "answer", a structured field. */
  label: string;
  type?: string;
}

function fields(v: unknown): Field[] {
  return Array.isArray(v) ? (v as Field[]).filter((f) => f && typeof f.name === "string" && f.name) : [];
}

/** The blocks with a path to b (b excluded). */
export function upstream(wf: Workflow, b: string): Block[] {
  const into = new Map<string, string[]>();
  for (const e of wf.edges) into.set(e.to.node, [...(into.get(e.to.node) ?? []), e.from.node]);

  const seen = new Set<string>();
  const stack = [...(into.get(b) ?? [])];
  while (stack.length) {
    const id = stack.pop()!;
    if (seen.has(id) || id === b) continue;
    seen.add(id);
    stack.push(...(into.get(id) ?? []));
  }
  return wf.nodes.filter((n) => seen.has(n.id));
}

/** The values a block produces for the blocks after it. */
export function produced(n: Block): Value[] {
  switch (n.type) {
    case "start":
      return fields(n.params?.inputs).map((f) => ({
        ref: { root: "start", path: f.name },
        source: "Start",
        label: f.name,
        type: f.type ?? "text",
      }));
    case "agent":
    case "router": {
      const out = fields(n.params?.output);
      if (out.length === 0) return [{ ref: { root: n.id, path: "text" }, source: n.name, label: "answer", type: "text" }];
      return out.map((f) => ({ ref: { root: n.id, path: f.name }, source: n.name, label: f.name, type: f.type ?? "text" }));
    }
    case "loop":
      return [{ ref: { root: n.id, path: "" }, source: n.name, label: "all answers", type: "list" }];
    case "merge":
      return [{ ref: { root: n.id, path: "" }, source: n.name, label: "branch answers", type: "object" }];
    case "team":
    case "subworkflow":
      return [{ ref: { root: n.id, path: "" }, source: n.name, label: "result", type: "object" }];
    default:
      return [];
  }
}

/** Everything block b may refer to. */
export function available(wf: Workflow, b: Block): Value[] {
  const out: Value[] = [];
  const start = wf.nodes.find((n) => n.type === "start");
  if (start) out.push(...produced(start));
  for (const n of upstream(wf, b.id)) if (n.type !== "start") out.push(...produced(n));
  if (b.type === "loop") out.unshift({ ref: { root: "item", path: "" }, source: "Current item", label: "item", type: "object" });
  return out;
}

/** How a reference reads in the editor. */
export function describe(wf: Workflow, r: Ref): { text: string; missing: boolean } {
  if (r.root === "start") return { text: `Start › ${r.path || "inputs"}`, missing: false };
  if (r.root === "item") return { text: r.path ? `item › ${r.path}` : "item", missing: false };
  const n = wf.nodes.find((x) => x.id === r.root);
  if (!n) return { text: "deleted block", missing: true };
  const label = r.path === "text" ? "answer" : r.path || "result";
  return { text: `${n.name} › ${label}`, missing: false };
}
