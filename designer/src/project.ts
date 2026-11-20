// The designer's document, mirroring internal/design/project (Go). The Go
// side decodes it strictly: params must hold only the keys its structs know.

export interface Project {
  version: 1;
  name: string;
  namespace?: string;
  credentials: Credential[];
  workflows: Workflow[];
}

export interface Credential {
  id: string;
  name: string;
  provider: string;
  env?: string;
  base_url?: string;
}

export interface Workflow {
  id: string;
  name: string;
  nodes: Block[];
  edges: Edge[];
}

export interface Block {
  id: string;
  type: BlockType;
  name: string;
  position: { x: number; y: number };
  params?: Record<string, unknown>;
  attachments?: Attachment[];
}

export interface Attachment {
  id: string;
  type: AttachmentType;
  name: string;
  params?: Record<string, unknown>;
}

export interface Edge {
  id: string;
  from: { node: string; port: string };
  to: { node: string };
}

export type BlockType =
  | "start"
  | "agent"
  | "router"
  | "condition"
  | "loop"
  | "parallel"
  | "merge"
  | "approval"
  | "subworkflow"
  | "team"
  | "output";

export type AttachmentType = "files" | "shell" | "kv" | "mcp" | "skills" | "knowledge";

export type FieldType = "text" | "number" | "boolean" | "list" | "object";

export interface Field {
  name: string;
  type?: FieldType;
  description?: string;
}

export interface Mapping {
  name: string;
  value: string;
}

export interface Problem {
  workflow?: string;
  node?: string;
  attachment?: string;
  field?: string;
  msg: string;
  path?: string;
}

let seq = 0;

/** A short id, unique within this page. */
export function newId(prefix: string): string {
  seq += 1;
  return `${prefix}${Date.now().toString(36)}${seq.toString(36)}`;
}

export function newWorkflow(name: string): Workflow {
  return {
    id: newId("w"),
    name,
    nodes: [
      { id: newId("n"), type: "start", name: "Start", position: { x: 0, y: 120 }, params: { inputs: [] } },
      { id: newId("n"), type: "output", name: "Output", position: { x: 640, y: 120 }, params: { fields: [] } },
    ],
    edges: [],
  };
}

export function newProject(name = "My project"): Project {
  return { version: 1, name, credentials: [], workflows: [newWorkflow("Main")] };
}

/** Removes empty values, so params hold only what the user set. */
export function clean<T extends Record<string, unknown>>(o: T): T {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(o)) {
    if (v === undefined || v === null || v === "" || v === false) continue;
    if (Array.isArray(v) && v.length === 0) continue;
    out[k] = v;
  }
  return out as T;
}

// References: {{ref <root> <path>}}, root being "start", "item" or a block id.

export const REF = /\{\{\s*ref\s+([A-Za-z0-9_-]+)(?:\s+([A-Za-z0-9_.]+))?\s*\}\}/g;

export interface Ref {
  root: string;
  path: string;
}

export function refToken(r: Ref): string {
  return r.path ? `{{ref ${r.root} ${r.path}}}` : `{{ref ${r.root}}}`;
}

/** The single reference s is made of, if it is exactly one. */
export function onlyRef(s: unknown): Ref | null {
  if (typeof s !== "string") return null;
  const m = s.trim().match(/^\{\{\s*ref\s+([A-Za-z0-9_-]+)(?:\s+([A-Za-z0-9_.]+))?\s*\}\}$/);
  return m ? { root: m[1], path: m[2] ?? "" } : null;
}

/** Splits text into plain parts and references. */
export function splitRefs(s: string): (string | Ref)[] {
  const out: (string | Ref)[] = [];
  let last = 0;
  for (const m of s.matchAll(REF)) {
    if (m.index! > last) out.push(s.slice(last, m.index));
    out.push({ root: m[1], path: m[2] ?? "" });
    last = m.index! + m[0].length;
  }
  if (last < s.length) out.push(s.slice(last));
  return out;
}

/** A name that works as an identifier in the fleet: lower-case, digits, _. */
export function identifier(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
}
