// The trail: a flow drawn left to right, with the path an execution walked
// laid down like a pheromone trail. Walked edges darken and thicken with
// visits; the edge into the step that is working now carries marching ants.

import { useMemo } from "react";

import type { Graph, NodeStatus } from "./api";
import { layoutTrail, type PlacedEdge, type PlacedNode } from "./trail";

interface Props {
  graph: Graph;
  nodes?: Record<string, NodeStatus>;
  /** Mini trails are dots on a line, for lists. */
  mini?: boolean;
  selected?: string;
  onSelect?: (node: string) => void;
}

const FULL = { colW: 196, rowH: 92, w: 152, h: 50, pad: 28 };
const MINI = { colW: 26, rowH: 16, w: 10, h: 10, pad: 7 };

const ACTIVE = new Set(["running", "retrying", "waiting"]);

export function Trail({ graph, nodes = {}, mini, selected, onSelect }: Props) {
  const layout = useMemo(() => layoutTrail(graph), [graph]);
  const g = mini ? MINI : FULL;
  const hasBack = layout.edges.some((e) => e.back);
  const width = g.pad * 2 + (layout.cols - 1) * g.colW + g.w;
  const height = g.pad * 2 + (layout.rows - 1) * g.rowH + g.h + (hasBack ? (mini ? 8 : 36) : 0);

  const at = new Map(
    layout.nodes.map((n) => [n.id, { x: g.pad + n.col * g.colW, y: g.pad + n.row * g.rowH, n }]),
  );
  const state = (id: string) => nodes[id]?.state ?? "idle";

  return (
    <svg
      className={mini ? "trail trail-mini" : "trail"}
      viewBox={`0 0 ${width} ${height}`}
      width={mini ? width : undefined}
      height={mini ? height : undefined}
      role="img"
      aria-label={trailLabel(graph, nodes)}
    >
      <g className="trail-edges">
        {layout.edges.map((e) => {
          const a = at.get(e.from)!;
          const b = at.get(e.to)!;
          const laid = state(e.from) !== "idle" && state(e.to) !== "idle";
          const live = laid && ACTIVE.has(state(e.to)) && !ACTIVE.has(state(e.from));
          const visits = Math.min(nodes[e.to]?.visits ?? 0, 4);
          return (
            <g key={e.from + ">" + e.to} className={`edge edge-${e.kind}${laid ? " laid" : ""}${live ? " live" : ""}`}>
              <path
                d={edgePath(a, b, e, g, height)}
                style={laid && !mini ? { strokeWidth: 2 + visits } : undefined}
              />
              {!mini && e.label && <EdgeLabel a={a} b={b} e={e} g={g} height={height} />}
            </g>
          );
        })}
      </g>
      <g className="trail-nodes">
        {layout.nodes.map((n) => {
          const p = at.get(n.id)!;
          const st = nodes[n.id];
          return mini ? (
            <circle key={n.id} className={`dot s-${state(n.id)}`} cx={p.x + g.w / 2} cy={p.y + g.h / 2} r={g.w / 2}>
              <title>{`${n.id}: ${state(n.id)}`}</title>
            </circle>
          ) : (
            <NodeShape
              key={n.id}
              n={n}
              x={p.x}
              y={p.y}
              st={st}
              start={n.id === graph.start}
              selected={selected === n.id}
              onSelect={onSelect}
            />
          );
        })}
      </g>
    </svg>
  );
}

type Geo = typeof FULL;
type Pos = { x: number; y: number; n: PlacedNode };

function edgePath(a: Pos, b: Pos, e: PlacedEdge, g: Geo, height: number): string {
  if (e.back || e.from === e.to) {
    // Back along the bottom: down from the source, across, up into the target.
    const x1 = a.x + g.w / 2;
    const x2 = b.x + g.w / 2;
    const y1 = a.y + g.h;
    const y2 = b.y + g.h;
    const low = height - 6;
    if (e.from === e.to) return `M ${x1 - 12} ${y1} C ${x1 - 24} ${low}, ${x1 + 24} ${low}, ${x1 + 12} ${y1}`;
    return `M ${x1} ${y1} C ${x1} ${low}, ${x2} ${low}, ${x2} ${y2}`;
  }
  const x1 = a.x + g.w;
  const y1 = a.y + g.h / 2;
  const x2 = b.x;
  const y2 = b.y + g.h / 2;
  const dx = Math.max(16, (x2 - x1) / 2);
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
}

function EdgeLabel({ a, b, e, g, height }: { a: Pos; b: Pos; e: PlacedEdge; g: Geo; height: number }) {
  const x = e.back ? (a.x + b.x + g.w) / 2 : (a.x + g.w + b.x) / 2;
  const y = e.back ? height - 12 : (a.y + b.y + g.h) / 2 - 6;
  return (
    <text className="edge-label" x={x} y={y} textAnchor="middle">
      {e.label!.length > 22 ? e.label!.slice(0, 21) + "…" : e.label}
    </text>
  );
}

const ROLE_WORD: Record<string, string> = { agent: "agent", activity: "activity", remote: "remote agent" };

function NodeShape(props: {
  n: PlacedNode;
  x: number;
  y: number;
  st?: NodeStatus;
  start: boolean;
  selected: boolean;
  onSelect?: (node: string) => void;
}) {
  const { n, x, y, st, start, selected, onSelect } = props;
  const g = FULL;
  const state = st?.state ?? "idle";
  const sub = n.runs ?? n.detail ?? n.type;
  const badge = badgeOf(st);

  return (
    <g
      className={`node t-${n.type} s-${state}${selected ? " selected" : ""}${onSelect ? " clickable" : ""}`}
      transform={`translate(${x} ${y})`}
      tabIndex={onSelect ? 0 : undefined}
      role={onSelect ? "button" : undefined}
      aria-label={`${n.id}, ${state}`}
      onClick={() => onSelect?.(n.id)}
      onKeyDown={(e) => {
        if (onSelect && (e.key === "Enter" || e.key === " ")) {
          e.preventDefault();
          onSelect(n.id);
        }
      }}
    >
      {ACTIVE.has(state) && <rect className="halo" x={-6} y={-6} width={g.w + 12} height={g.h + 12} rx={16} />}
      <path className="body" d={shapePath(n.type, g.w, g.h)} />
      {n.type === "subflow" && <rect className="inner" x={4} y={4} width={g.w - 8} height={g.h - 8} rx={8} />}
      {start && <circle className="start-mark" cx={-10} cy={g.h / 2} r={4} />}
      <text className="node-id" x={14} y={22}>
        {clip(n.id, 17)}
      </text>
      <text className="node-sub" x={14} y={38}>
        {clip(n.role ? `${ROLE_WORD[n.role]} ${sub}` : sub, 21)}
      </text>
      {n.human && (
        <g className="human-mark" transform={`translate(${g.w - 20} 12)`}>
          <title>stops for a human</title>
          <circle cx={4} cy={3} r={3} />
          <path d="M -1 13 a 5 5 0 0 1 10 0 z" />
        </g>
      )}
      {badge && (
        <g className="badge" transform={`translate(${g.w - 4} ${g.h - 2})`}>
          <rect x={-badge.length * 6.4 - 8} y={-9} width={badge.length * 6.4 + 12} height={17} rx={8.5} />
          <text x={-badge.length * 3.2 - 2} y={4} textAnchor="middle">
            {badge}
          </text>
        </g>
      )}
      <title>{titleOf(n, st)}</title>
    </g>
  );
}

function shapePath(type: string, w: number, h: number): string {
  switch (type) {
    case "choice": {
      const k = 14;
      return `M ${k} 0 H ${w - k} L ${w} ${h / 2} L ${w - k} ${h} H ${k} L 0 ${h / 2} Z`;
    }
    case "fanout":
    case "join": {
      const k = 10;
      return `M 0 ${k} Q 0 0 ${k} 0 H ${w - k} Q ${w} 0 ${w} ${k} V ${h - k} Q ${w} ${h} ${w - k} ${h} H ${k} Q 0 ${h} 0 ${h - k} Z`;
    }
    case "await":
      return roundRect(w, h, h / 2);
    default:
      return roundRect(w, h, 12);
  }
}

function roundRect(w: number, h: number, r: number): string {
  return `M ${r} 0 H ${w - r} A ${r} ${r} 0 0 1 ${w} ${r} V ${h - r} A ${r} ${r} 0 0 1 ${w - r} ${h} H ${r} A ${r} ${r} 0 0 1 0 ${h - r} V ${r} A ${r} ${r} 0 0 1 ${r} 0 Z`;
}

function badgeOf(st?: NodeStatus): string | undefined {
  if (!st) return undefined;
  if (st.total) return `${st.done ?? 0}/${st.total}`;
  if (st.state === "retrying" && st.attempt) return `try ${st.attempt}`;
  if ((st.visits ?? 0) > 1) return `×${st.visits}`;
  return undefined;
}

function titleOf(n: PlacedNode, st?: NodeStatus): string {
  const parts = [`${n.id} (${n.type})`];
  if (n.runs) parts.push(`runs ${n.runs}`);
  if (st) parts.push(st.state);
  if (st?.error) parts.push(st.error);
  return parts.join("\n");
}

function trailLabel(g: Graph, nodes: Record<string, NodeStatus>): string {
  const active = Object.entries(nodes)
    .filter(([, s]) => s.state !== "idle" && s.state !== "done")
    .map(([id, s]) => `${id} ${s.state}`);
  return `Flow ${g.flow}, ${g.nodes.length} steps${active.length ? ": " + active.join(", ") : ""}`;
}

function clip(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}
