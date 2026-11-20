// "Tidy up": arranges a workflow's blocks left to right.

import ELK from "elkjs/lib/elk.bundled.js";

import type { Workflow } from "./project";

const elk = new ELK();

export async function tidy(wf: Workflow): Promise<Workflow> {
  const size = (id: string) => {
    const el = document.querySelector(`.react-flow__node[data-id="${CSS.escape(id)}"]`) as HTMLElement | null;
    return { width: el?.offsetWidth || 240, height: el?.offsetHeight || 70 };
  };

  const res = await elk.layout({
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      "elk.spacing.nodeNode": "40",
      "elk.layered.spacing.nodeNodeBetweenLayers": "80",
      "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
    },
    children: wf.nodes.map((n) => ({ id: n.id, ...size(n.id) })),
    edges: wf.edges.map((e) => ({ id: e.id, sources: [e.from.node], targets: [e.to.node] })),
  });

  const at = new Map((res.children ?? []).map((c) => [c.id, { x: Math.round(c.x ?? 0), y: Math.round(c.y ?? 0) }]));
  return { ...wf, nodes: wf.nodes.map((n) => ({ ...n, position: at.get(n.id) ?? n.position })) };
}
