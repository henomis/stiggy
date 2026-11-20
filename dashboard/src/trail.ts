// A flow laid out left to right: columns by depth from the start, rows within
// a column by discovery order. Edges that go back (review loops, retries
// through a router) are kept and drawn as arcs.

import type { Graph, GraphEdge, GraphNode } from "./api";

export interface PlacedNode extends GraphNode {
  col: number;
  row: number;
}

export interface PlacedEdge extends GraphEdge {
  back: boolean;
}

export interface TrailLayout {
  nodes: PlacedNode[];
  edges: PlacedEdge[];
  cols: number;
  rows: number;
}

export function layoutTrail(g: Graph): TrailLayout {
  const ids = g.nodes.map((n) => n.id);
  const out = new Map<string, GraphEdge[]>();
  for (const id of ids) out.set(id, []);
  for (const e of g.edges) if (out.has(e.from) && out.has(e.to)) out.get(e.from)!.push(e);

  // Depth-first from the start, then from anything unreached: an edge to a
  // node still on the stack goes back.
  const back = new Set<GraphEdge>();
  const order: string[] = [];
  const state = new Map<string, 1 | 2>();
  const visit = (id: string) => {
    state.set(id, 1);
    order.push(id);
    for (const e of out.get(id)!) {
      const s = state.get(e.to);
      if (s === 1) back.add(e);
      else if (s === undefined) visit(e.to);
    }
    state.set(id, 2);
  };
  if (out.has(g.start)) visit(g.start);
  for (const id of ids) if (!state.has(id)) visit(id);

  // Longest path over forward edges, in topological order.
  const indeg = new Map(ids.map((id) => [id, 0]));
  for (const e of g.edges) if (!back.has(e) && indeg.has(e.to) && out.has(e.from)) indeg.set(e.to, indeg.get(e.to)! + 1);
  const col = new Map(ids.map((id) => [id, 0]));
  const queue = order.filter((id) => indeg.get(id) === 0);
  while (queue.length) {
    const id = queue.shift()!;
    for (const e of out.get(id)!) {
      if (back.has(e)) continue;
      col.set(e.to, Math.max(col.get(e.to)!, col.get(id)! + 1));
      indeg.set(e.to, indeg.get(e.to)! - 1);
      if (indeg.get(e.to) === 0) queue.push(e.to);
    }
  }

  const rowsIn = new Map<number, number>();
  const row = new Map<string, number>();
  for (const id of order) {
    const c = col.get(id)!;
    const r = rowsIn.get(c) ?? 0;
    row.set(id, r);
    rowsIn.set(c, r + 1);
  }

  const rows = Math.max(1, ...rowsIn.values());
  const cols = Math.max(1, ...[...col.values()].map((c) => c + 1));
  const byId = new Map(g.nodes.map((n) => [n.id, n]));

  return {
    // Columns with fewer nodes are centred on the tallest.
    nodes: order.map((id) => {
      const c = col.get(id)!;
      return { ...byId.get(id)!, col: c, row: row.get(id)! + (rows - rowsIn.get(c)!) / 2 };
    }),
    edges: g.edges.filter((e) => byId.has(e.from) && byId.has(e.to)).map((e) => ({ ...e, back: back.has(e) })),
    cols,
    rows,
  };
}
