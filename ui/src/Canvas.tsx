// The workflow canvas. Blocks sit where the user puts them; connections go
// from a named output port to a block. Blocks come from the palette by drag
// and drop; tools and knowledge bases dropped onto an AI block attach to it.

import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  ReactFlowProvider,
  applyNodeChanges,
  useReactFlow,
  type Connection,
  type Edge as RFEdge,
  type Node,
  type NodeChange,
  type NodeProps,
} from "@xyflow/react";
import { useEffect, useMemo, useState } from "react";

import { ATTACHMENT, BLOCK } from "./blocks";
import type { AttachmentType, Block, BlockType, Workflow } from "./project";

export const DND_BLOCK = "application/x-stiggy-block";
export const DND_ATTACHMENT = "application/x-stiggy-attachment";

type Data = { block: Block; errors: number; selected: boolean } & Record<string, unknown>;

function BlockNode({ data }: NodeProps<Node<Data>>) {
  const { block, errors, selected } = data;
  const def = BLOCK[block.type];
  const ports = def.ports(block);

  return (
    <div className={`block${selected ? " selected" : ""}${errors ? " has-error" : ""}`} data-ai={def.ai ? "1" : undefined}>
      {def.hasInput && <Handle type="target" position={Position.Left} className="in" />}
      <div className="block-main">
        <span className="block-icon" style={{ background: `var(${def.color})` }}>
          {def.icon}
        </span>
        <div className="block-text">
          <div className="block-name">{block.name}</div>
          <div className="block-summary">{def.summary(block)}</div>
        </div>
        {errors > 0 && (
          <span className="badge" title={`${errors} problem(s)`}>
            {errors}
          </span>
        )}
      </div>
      {(block.attachments?.length ?? 0) > 0 && (
        <div className="block-attachments">
          {block.attachments!.map((a) => (
            <span key={a.id} className="mini" title={a.name}>
              {ATTACHMENT[a.type].icon} {a.name}
            </span>
          ))}
        </div>
      )}
      {ports.length === 1 && ports[0].id === "out" ? (
        <Handle type="source" id="out" position={Position.Right} className="out" />
      ) : (
        <div className="ports">
          {ports.map((p) => (
            <div className="port" key={p.id}>
              <span>{p.label ?? p.id}</span>
              <Handle type="source" id={p.id} position={Position.Right} className="out" />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

const nodeTypes = { block: BlockNode };

interface Props {
  wf: Workflow;
  selected: string | null;
  errors: Map<string, number>;
  onSelect: (id: string | null) => void;
  onChange: (wf: Workflow) => void;
  onDropBlock: (type: BlockType, at: { x: number; y: number }) => void;
  onAttach: (block: string, type: AttachmentType) => void;
}

export function Canvas(props: Props) {
  return (
    <ReactFlowProvider>
      <Inner {...props} />
    </ReactFlowProvider>
  );
}

function Inner({ wf, selected, errors, onSelect, onChange, onDropBlock, onAttach }: Props) {
  const rf = useReactFlow();

  // React Flow keeps measured sizes and live drag positions in node state;
  // the workflow gets the position when a drag ends.
  const [nodes, setNodes] = useState<Node<Data>[]>([]);
  useEffect(() => {
    setNodes((prev) => {
      const old = new Map(prev.map((n) => [n.id, n]));
      return wf.nodes.map((b) => ({
        ...old.get(b.id),
        id: b.id,
        type: "block",
        position: b.position,
        data: { block: b, errors: errors.get(b.id) ?? 0, selected: b.id === selected },
        deletable: !BLOCK[b.type].single,
      }));
    });
  }, [wf, errors, selected]);

  // A block selected off-screen (one just added, or picked from the problem
  // list) is brought into view.
  useEffect(() => {
    const b = wf.nodes.find((n) => n.id === selected);
    const box = document.querySelector(".canvas")?.getBoundingClientRect();
    if (!b || !box) return;
    const { x, y, zoom } = rf.getViewport();
    const sx = b.position.x * zoom + x;
    const sy = b.position.y * zoom + y;
    if (sx < 0 || sy < 0 || sx + 240 * zoom > box.width || sy + 60 * zoom > box.height) {
      rf.setCenter(b.position.x + 120, b.position.y + 30, { zoom: Math.max(zoom, 0.8), duration: 250 });
    }
    // Only when the selection changes, not on every edit.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected]);

  const edges: RFEdge[] = useMemo(
    () =>
      wf.edges.map((e) => ({
        id: e.id,
        source: e.from.node,
        sourceHandle: e.from.port,
        target: e.to.node,
        markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
        style: { strokeWidth: 1.6 },
      })),
    [wf.edges],
  );

  const onConnect = (c: Connection) => {
    if (!c.source || !c.target || c.source === c.target) return;
    const port = c.sourceHandle ?? "out";
    // One port leads to one block: a new connection replaces the old one.
    const kept = wf.edges.filter((e) => !(e.from.node === c.source && e.from.port === port));
    onChange({
      ...wf,
      edges: [...kept, { id: `e${Date.now().toString(36)}`, from: { node: c.source, port }, to: { node: c.target } }],
    });
  };

  return (
    <div
      className="canvas"
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes(DND_BLOCK) || e.dataTransfer.types.includes(DND_ATTACHMENT)) {
          e.preventDefault();
          e.dataTransfer.dropEffect = "copy";
        }
      }}
      onDrop={(e) => {
        const block = e.dataTransfer.getData(DND_BLOCK) as BlockType;
        const att = e.dataTransfer.getData(DND_ATTACHMENT) as AttachmentType;
        if (!block && !att) return;
        e.preventDefault();
        if (att) {
          const el = (document.elementFromPoint(e.clientX, e.clientY) as HTMLElement | null)?.closest(".react-flow__node");
          const id = el?.getAttribute("data-id");
          const b = wf.nodes.find((n) => n.id === id);
          if (b && BLOCK[b.type].ai) onAttach(b.id, att);
          return;
        }
        const at = rf.screenToFlowPosition({ x: e.clientX, y: e.clientY });
        onDropBlock(block, { x: at.x - 110, y: at.y - 30 });
      }}
    >
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={(ch: NodeChange<Node<Data>>[]) => {
          setNodes((ns) => applyNodeChanges(ch.filter((c) => c.type !== "remove"), ns));
          const moved = ch.filter((c) => c.type === "position" && c.dragging === false && c.position);
          if (moved.length) {
            const at = new Map(moved.map((c) => [(c as { id: string }).id, (c as { position: { x: number; y: number } }).position]));
            onChange({ ...wf, nodes: wf.nodes.map((n) => (at.has(n.id) ? { ...n, position: at.get(n.id)! } : n)) });
          }
        }}
        onConnect={onConnect}
        isValidConnection={(c) => c.source !== c.target && wf.nodes.find((n) => n.id === c.target)?.type !== "start"}
        onNodesDelete={(ns) => {
          const gone = new Set(ns.map((n) => n.id));
          onChange({
            ...wf,
            nodes: wf.nodes.filter((n) => !gone.has(n.id)),
            edges: wf.edges.filter((e) => !gone.has(e.from.node) && !gone.has(e.to.node)),
          });
          onSelect(null);
        }}
        onEdgesDelete={(es) => {
          const gone = new Set(es.map((e) => e.id));
          onChange({ ...wf, edges: wf.edges.filter((e) => !gone.has(e.id)) });
        }}
        onNodeClick={(_, n) => onSelect(n.id)}
        onPaneClick={() => onSelect(null)}
        deleteKeyCode={["Backspace", "Delete"]}
        fitView
        fitViewOptions={{ padding: 0.25, maxZoom: 1 }}
        proOptions={{ hideAttribution: true }}
        minZoom={0.2}
        snapToGrid
        snapGrid={[10, 10]}
      >
        <Background gap={20} size={1} color="var(--grid)" />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}
