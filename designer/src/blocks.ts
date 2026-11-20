// The block catalogue: what each block is called, where it sits in the
// palette, its ports, its starting settings and its one-line summary.

import type { Attachment, AttachmentType, Block, BlockType, Project } from "./project";
import { newId } from "./project";

export interface Port {
  id: string;
  label?: string;
}

export interface BlockDef {
  type: BlockType;
  label: string;
  group: "Start & end" | "AI" | "Flow" | "Teams";
  icon: string;
  /** A token from styles.css: --c-ai, --c-flow, --c-io, --c-team. */
  color: string;
  description: string;
  /** True for blocks that run an agent and can take tools and knowledge. */
  ai?: boolean;
  /** Only one per workflow. */
  single?: boolean;
  hasInput: boolean;
  ports: (b: Block) => Port[];
  defaults: (p: Project) => Record<string, unknown>;
  summary: (b: Block) => string;
}

const out: Port[] = [{ id: "out" }];

const str = (v: unknown) => (typeof v === "string" ? v : "");
const list = (v: unknown) => (Array.isArray(v) ? v : []);

/** Text with references shortened, for summaries. */
export function plain(s: unknown): string {
  return str(s)
    .replace(/\{\{\s*ref\s+[^}]*\}\}/g, "…")
    .replace(/\s+/g, " ")
    .trim();
}

const agentDefaults = (p: Project) => ({ credential: p.credentials[0]?.id ?? "", model: "", prompt: "" });

export const BLOCKS: BlockDef[] = [
  {
    type: "start",
    label: "Start",
    group: "Start & end",
    icon: "▶",
    color: "--c-io",
    description: "Where the workflow begins, and the inputs it is given.",
    single: true,
    hasInput: false,
    ports: () => out,
    defaults: () => ({ inputs: [] }),
    summary: (b) => {
      const ins = list(b.params?.inputs).map((f) => (f as { name: string }).name);
      return ins.length ? `inputs: ${ins.join(", ")}` : "no inputs";
    },
  },
  {
    type: "output",
    label: "Output",
    group: "Start & end",
    icon: "■",
    color: "--c-io",
    description: "What the workflow returns.",
    single: true,
    hasInput: true,
    ports: () => [],
    defaults: () => ({ fields: [] }),
    summary: (b) => {
      const fs = list(b.params?.fields).map((f) => (f as { name: string }).name);
      return fs.length ? `returns ${fs.join(", ")}` : "returns the last answer";
    },
  },
  {
    type: "agent",
    label: "AI Agent",
    group: "AI",
    icon: "✦",
    color: "--c-ai",
    description: "A model with instructions does one task. It can use tools and knowledge.",
    ai: true,
    hasInput: true,
    ports: () => out,
    defaults: agentDefaults,
    summary: (b) => plain(b.params?.prompt) || "no task yet",
  },
  {
    type: "router",
    label: "AI Router",
    group: "AI",
    icon: "⑂",
    color: "--c-ai",
    description: "An agent reads the situation and picks the next step.",
    ai: true,
    hasInput: true,
    ports: (b) => [
      ...list(b.params?.routes).map((r) => ({ id: (r as { port: string }).port })),
      { id: "otherwise" },
    ],
    defaults: (p) => ({
      ...agentDefaults(p),
      routes: [
        { port: "route_a", when: "" },
        { port: "route_b", when: "" },
      ],
    }),
    summary: (b) => `${list(b.params?.routes).length} routes`,
  },
  {
    type: "loop",
    label: "Loop over items",
    group: "AI",
    icon: "↻",
    color: "--c-ai",
    description: "Runs an agent once for each item of a list, in parallel.",
    ai: true,
    hasInput: true,
    ports: () => out,
    defaults: (p) => ({ ...agentDefaults(p), over: "", max_parallel: 4 }),
    summary: (b) => plain(b.params?.prompt) || "for each item",
  },
  {
    type: "condition",
    label: "Condition",
    group: "Flow",
    icon: "◇",
    color: "--c-flow",
    description: "Goes one way or the other depending on values.",
    hasInput: true,
    ports: () => [{ id: "true" }, { id: "false" }],
    defaults: () => ({ match: "all", rules: [{ left: "", op: "equals", value: "" }] }),
    summary: (b) => `${list(b.params?.rules).length} rule(s), match ${str(b.params?.match) || "all"}`,
  },
  {
    type: "parallel",
    label: "Parallel",
    group: "Flow",
    icon: "⇉",
    color: "--c-flow",
    description: "Runs branches side by side. Each branch is one block that leads to a Merge.",
    hasInput: true,
    ports: (b) => list(b.params?.branches).map((x) => ({ id: String(x) })),
    defaults: () => ({ branches: ["branch_1", "branch_2"] }),
    summary: (b) => `${list(b.params?.branches).length} branches`,
  },
  {
    type: "merge",
    label: "Merge",
    group: "Flow",
    icon: "⇶",
    color: "--c-flow",
    description: "Waits for parallel branches: all of them, the first to finish, or all of them with some allowed to fail.",
    hasInput: true,
    ports: () => out,
    defaults: () => ({ wait: "all" }),
    summary: (b) =>
      b.params?.wait === "first"
        ? "wait for the first"
        : b.params?.wait === "min"
          ? `wait for all, ${typeof b.params?.min === "number" ? b.params.min : 1} must succeed`
          : "wait for all",
  },
  {
    type: "approval",
    label: "Human approval",
    group: "Flow",
    icon: "✋",
    color: "--c-flow",
    description: "Pauses until a person approves or rejects.",
    hasInput: true,
    ports: () => [{ id: "approved" }, { id: "rejected" }, { id: "timeout", label: "timed out" }],
    defaults: () => ({ message: "", timeout: "24h" }),
    summary: (b) => plain(b.params?.message) || `waits up to ${str(b.params?.timeout) || "24h"}`,
  },
  {
    type: "subworkflow",
    label: "Run workflow",
    group: "Flow",
    icon: "⧉",
    color: "--c-flow",
    description: "Runs another workflow of this project and uses its result.",
    hasInput: true,
    ports: () => out,
    defaults: () => ({ workflow: "", inputs: [] }),
    summary: () => "runs a workflow",
  },
  {
    type: "team",
    label: "Agent team",
    group: "Teams",
    icon: "✺",
    color: "--c-team",
    description: "Several agents work together on one task: a crew, a supervisor and workers, a debate…",
    hasInput: true,
    ports: () => out,
    defaults: (p) => {
      const cred = p.credentials[0]?.id ?? "";
      return {
        kind: "supervisor",
        task: "",
        lead: "Lead",
        members: [
          { name: "Lead", credential: cred, model: "", instructions: "You coordinate the team." },
          { name: "Worker", credential: cred, model: "", instructions: "" },
        ],
      };
    },
    summary: (b) => `${TEAM_KINDS[str(b.params?.kind)]?.label ?? "team"} · ${list(b.params?.members).length} members`,
  },
];

export const BLOCK = Object.fromEntries(BLOCKS.map((b) => [b.type, b])) as Record<BlockType, BlockDef>;

export const TEAM_KINDS: Record<string, { label: string; description: string; roles: string[] }> = {
  supervisor: { label: "Supervisor", description: "A lead hands work to the others and decides when it is done.", roles: ["lead"] },
  crew: { label: "Crew", description: "Members do a list of tasks, each building on the earlier ones.", roles: [] },
  swarm: { label: "Swarm", description: "Members hand the task to each other until one answers.", roles: [] },
  evaluator_optimizer: {
    label: "Write & review",
    description: "One writes, another reviews, until the reviewer is satisfied.",
    roles: ["generator", "evaluator"],
  },
  debate: { label: "Debate", description: "Members argue for rounds; a judge decides.", roles: ["judge"] },
  plan_execute: {
    label: "Plan & execute",
    description: "A planner splits the task, an executor does each part, a synthesizer combines them.",
    roles: ["planner", "executor", "synthesizer"],
  },
};

export interface AttachmentDef {
  type: AttachmentType;
  label: string;
  icon: string;
  description: string;
  defaults: () => Record<string, unknown>;
  summary: (a: Attachment) => string;
}

export const ATTACHMENTS: AttachmentDef[] = [
  {
    type: "knowledge",
    label: "Knowledge base",
    icon: "📚",
    description: "Documents the agent can search.",
    defaults: () => ({ documents: "", store: "qdrant", store_options: { host: "localhost", collection: "docs", vector_size: 768 } }),
    summary: (a) => str(a.params?.documents) || "no documents yet",
  },
  {
    type: "files",
    label: "Files",
    icon: "📄",
    description: "Read, write, edit and search files in a folder.",
    defaults: () => ({ working_dir: "", read: true, search: true }),
    summary: (a) => {
      const can = ["read", "write", "edit", "search"].filter((k) => a.params?.[k]);
      return can.join(", ") || "nothing allowed";
    },
  },
  {
    type: "shell",
    label: "Shell",
    icon: "⌘",
    description: "Run commands, optionally from an allow-list.",
    defaults: () => ({ safe_mode: true }),
    summary: (a) => (list(a.params?.allowlist).length ? `allows ${list(a.params?.allowlist).join(", ")}` : "any command"),
  },
  {
    type: "mcp",
    label: "MCP server",
    icon: "⚡",
    description: "The tools of an MCP server.",
    defaults: () => ({ command: "" }),
    summary: (a) => str(a.params?.url) || str(a.params?.command) || "not set up",
  },
  {
    type: "kv",
    label: "Key-value store",
    icon: "🗄",
    description: "Remember values across runs.",
    defaults: () => ({ bucket: "" }),
    summary: (a) => str(a.params?.bucket) || "no bucket",
  },
  {
    type: "skills",
    label: "Skills",
    icon: "🧩",
    description: "Skills from a folder.",
    defaults: () => ({ root: "" }),
    summary: (a) => str(a.params?.root) || "no folder",
  },
];

export const ATTACHMENT = Object.fromEntries(ATTACHMENTS.map((a) => [a.type, a])) as Record<AttachmentType, AttachmentDef>;

export function newBlock(type: BlockType, p: Project, at: { x: number; y: number }, taken: string[]): Block {
  const def = BLOCK[type];
  let name = def.label;
  for (let i = 2; taken.includes(name); i++) name = `${def.label} ${i}`;
  return { id: newId("n"), type, name, position: at, params: def.defaults(p) };
}

export function newAttachment(type: AttachmentType): Attachment {
  const def = ATTACHMENT[type];
  return { id: newId("a"), type, name: def.label, params: def.defaults() };
}
