// The dashboard API (internal/dashboard). Every view is shaped by the
// server; the page only draws it.

export type Status = "running" | "waiting" | "completed" | "failed" | "cancelled" | "quarantined";

export interface Share {
  name: string;
  tokens_in: number;
  tokens_out: number;
  cost_usd: number;
}

export interface Usage {
  tokens_in: number;
  tokens_out: number;
  cache_read?: number;
  reasoning?: number;
  cost_usd: number;
  llm_calls?: number;
  tool_calls?: number;
  tool_errors?: number;
  agent_steps?: number;
  steps?: number;
  by_agent?: Share[];
  by_model?: Share[];
}

export interface Summary {
  namespace: string;
  exec_id: string;
  flow: string;
  status: Status;
  created: string;
  updated: string;
  error?: string;
  reason?: string;
  parent?: string;
  attrs?: Record<string, string>;
  /** What the execution was started with, in one line. */
  input?: string;
  usage?: Usage;
  nodes?: Record<string, NodeStatus>;
}

export interface Turn {
  kind: string;
  text: string;
  reply: string;
}

export interface Wait {
  node: string;
  agent?: string;
  kind: "question" | "review" | string;
  text: string;
  route?: string;
  route_reason?: string;
  asked_at?: string;
  turns?: Turn[];
}

export interface Waiting extends Summary {
  waits: Wait[];
}

export interface Bucket {
  hour: string;
  started: number;
  failed: number;
  finished: number;
}

export interface FlowStats {
  name: string;
  counts: Record<string, number>;
  last?: string;
}

export interface Namespace {
  name: string;
  counts: Record<string, number>;
  total: number;
  flows: FlowStats[];
  usage: Usage;
  sampled: number;
  throughput: Bucket[];
  recent: Summary[];
  failures: Summary[];
  error?: string;
}

export interface Agent {
  owner: string;
  name: string;
  framework: string;
  session?: string;
  instances: number;
}

export interface Overview {
  namespaces: Namespace[];
  waiting: Waiting[];
  agents: Agent[];
  agents_error?: string;
  read_only: boolean;
  generated: string;
}

export interface GraphNode {
  id: string;
  type: string;
  role?: "agent" | "activity" | "remote";
  runs?: string;
  human?: boolean;
  detail?: string;
}

export interface GraphEdge {
  from: string;
  to: string;
  kind: "next" | "route" | "rule" | "branch" | "failure" | "timeout";
  label?: string;
}

export interface Graph {
  flow: string;
  description?: string;
  start: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export type NodeState = "idle" | "done" | "running" | "retrying" | "waiting" | "failed";

export interface NodeStatus {
  state: NodeState;
  visits?: number;
  attempt?: number;
  error?: string;
  done?: number;
  total?: number;
  child?: string;
}

export interface Field {
  key: string;
  kind: "text" | "number" | "bool" | "list" | "object";
  text: string;
  items?: Field[];
}

export interface Execution extends Omit<Summary, "input"> {
  graph?: Graph;
  waits: Wait[];
  input: Field[];
  output: Field[];
  errors: { node: string; error: string; reason?: string; attempt?: number }[];
  children: { node: string; exec_id: string; flow: string }[];
  budget: { counter: string; used: number; limit: number }[];
  forked_from?: string;
  failed_node?: string;
  last_node?: string;
  steps: number;
  max_steps?: number;
  archived?: boolean;
  channels: Field[];
  /** The event the execution is shown at; absent: now. */
  seq?: number;
}

export interface Span {
  node: string;
  key: string;
  attempt?: number;
  start: string;
  end?: string;
  outcome: "running" | "done" | "cached" | "retry" | "failed" | "waiting" | "human" | "cancelled";
  error?: string;
  next?: string;
  by?: string;
  tokens?: number;
  cost_usd?: number;
}

export interface Timeline {
  start: string;
  end?: string;
  spans: Span[];
  marks: { time: string; node?: string; text: string }[];
}

export interface StepConfig {
  prompt?: string;
  expected_output?: string;
  routes?: string[];
  context?: string[];
  writes?: Record<string, string>;
  max_attempts?: number;
  backoff?: string;
  retry_delay?: string;
  timeout?: string;
  cached?: boolean;
  schema?: boolean;
}

export interface Run {
  key: string;
  visit: number;
  attempt: number;
  start: string;
  end?: string;
  outcome: Span["outcome"];
  error?: string;
  next?: string;
  output?: Field[];
  writes?: string[];
  usage?: Usage;
  resumed_by?: string;
  retry_at?: string;
}

export interface StepHistory {
  node: string;
  config?: StepConfig;
  runs: Run[];
  choices?: { time: string; node?: string; text: string }[];
}

export interface Exchange {
  node: string;
  agent?: string;
  kind: string;
  text: string;
  asked_at: string;
  reply?: string;
  approved?: boolean;
  by?: string;
  replied_at?: string;
}

export interface Moment {
  seq: number;
  time: string;
  text: string;
  node?: string;
}

export interface Entry {
  kind: "start" | "step" | "human" | "event" | "end";
  time: string;
  end?: string;
  node?: string;
  who?: string;
  role?: string;
  visit?: number;
  attempt?: number;
  outcome?: Span["outcome"];
  text?: string;
  asks?: string;
  output?: Field[];
  kept?: boolean;
  approved?: boolean;
  next?: string;
  error?: string;
  usage?: Usage;
  /** With attempt, identify the attempt whose record to fetch. */
  key?: string;
  generation?: number;
  /** The attempt stored a record of what it did (fetch it with record()). */
  recorded?: boolean;
}

// An attempt's record (agentrun.Record, mirrored in internal/dashboard).

export interface RecordUsage {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
  reasoning_tokens?: number;
  cost_usd?: number;
}

export interface RecordMessage {
  role: string;
  name?: string;
  text?: string;
  reasoning?: string;
  tool_calls?: string[];
  tool_call_id?: string;
  tool_error?: boolean;
  truncated?: boolean;
}

export interface RecordToolCall {
  tool: string;
  call_id?: string;
  args?: string;
  result?: string;
  error?: string;
  start?: string;
  end?: string;
  truncated?: boolean;
}

export interface RecordCall {
  start?: string;
  end?: string;
  ttft_ms?: number;
  iteration?: number;
  compaction?: boolean;
  model?: string;
  finish_reason?: string;
  usage: RecordUsage;
  message_offset: number;
  messages?: RecordMessage[];
  response?: string;
  response_truncated?: boolean;
  error?: string;
  tool_calls?: RecordToolCall[];
}

export interface RecordMemory {
  retrieved: number;
  saved: number;
  retrieved_items?: RecordMessage[];
  saved_items?: RecordMessage[];
}

export interface RecordRun {
  agent: string;
  via?: string;
  tool?: string;
  /** owner/name of the remote agent that ran the step (via "remote"). */
  remote?: string;
  model?: string;
  trace_id?: string;
  span_id?: string;
  system?: string;
  system_truncated?: boolean;
  response_format?: string;
  tools?: string[];
  output?: string;
  output_truncated?: boolean;
  start?: string;
  end?: string;
  stop_reason?: string;
  iterations?: number;
  max_iterations?: number;
  ttft_ms?: number;
  memory?: RecordMemory;
  usage: RecordUsage;
  calls?: RecordCall[];
  children?: RecordRun[];
  error?: string;
}

export interface StepRecord extends RecordRun {
  version: number;
  level: "prompt" | "full" | string;
  output_schema?: Record<string, unknown>;
  truncated?: boolean;
}

export interface AttemptRecord {
  key: string;
  generation: number;
  attempt: number;
  outcome: "completed" | "failed" | "interrupted" | string;
  seq: number;
  time: string;
  record?: StepRecord;
  /** packtrail cut the record: its size and the start of its JSON. */
  too_large?: { size: number; head: string };
  trace_url?: string;
}

export interface History {
  timeline: Timeline;
  steps: Record<string, StepHistory>;
  conversation: Exchange[];
  moments: Moment[];
  story: Entry[];
}

export interface Step {
  type: "text" | "tool_call" | "tool_result" | "compaction" | "todo" | string;
  agent: string;
  node: string;
  text?: string;
  tool?: string;
  args?: string;
  is_error?: boolean;
  todos?: { content?: string; text?: string; status?: string }[];
  key: string;
  attempt: number;
  time: string;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string }).error ?? res.statusText);
  return data as T;
}

const enc = encodeURIComponent;
const exec = (ns: string, id: string) => `/api/ns/${enc(ns)}/executions/${enc(id)}`;

export const overview = () => call<Overview>("GET", "/api/overview");

export const executions = (ns: string, q: { status?: string; flow?: string; limit?: number } = {}) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) if (v) p.set(k, String(v));
  return call<{ executions: Summary[] }>("GET", `/api/ns/${enc(ns)}/executions?${p}`).then((r) => r.executions);
};

export const execution = (ns: string, id: string, seq?: number) =>
  call<Execution>("GET", exec(ns, id) + (seq ? `?seq=${seq}` : ""));
export const history = (ns: string, id: string) => call<History>("GET", exec(ns, id) + "/history");
export const record = (ns: string, id: string, e: { node: string; key: string; generation: number; attempt: number }) => {
  const p = new URLSearchParams({ node: e.node, key: e.key, generation: String(e.generation), attempt: String(e.attempt) });
  return call<AttemptRecord>("GET", exec(ns, id) + `/records?${p}`);
};
export const flowGraph = (ns: string, flow: string) => call<Graph>("GET", `/api/ns/${enc(ns)}/flows/${enc(flow)}`);

export interface Reply {
  node: string;
  answer?: string;
  approve?: boolean;
  feedback?: string;
  by?: string;
}

export const resume = (ns: string, id: string, r: Reply) => call<{ ok: boolean }>("POST", exec(ns, id) + "/resume", r);
export const cancel = (ns: string, id: string, reason?: string) =>
  call<{ ok: boolean }>("POST", exec(ns, id) + "/cancel", { reason });

/** Streams one SSE endpoint; returns a function that closes it. */
export function stream<T>(path: string, event: string, on: (v: T) => void, onEnd?: () => void): () => void {
  const es = new EventSource(path);
  es.addEventListener(event, (e) => on(JSON.parse((e as MessageEvent).data) as T));
  es.addEventListener("end", () => {
    es.close();
    onEnd?.();
  });
  return () => es.close();
}

export const progressPath = (ns: string, id: string) => exec(ns, id) + "/progress";

/** Sums usages, shares included (by name, largest first). */
export class UsageSum {
  u: Usage = { tokens_in: 0, tokens_out: 0, cost_usd: 0, tool_calls: 0, by_agent: [], by_model: [] };

  add(o: Usage) {
    this.u.tokens_in += o.tokens_in;
    this.u.tokens_out += o.tokens_out;
    this.u.cost_usd += o.cost_usd;
    this.u.tool_calls = (this.u.tool_calls ?? 0) + (o.tool_calls ?? 0);
    this.u.by_agent = merge(this.u.by_agent ?? [], o.by_agent ?? []);
    this.u.by_model = merge(this.u.by_model ?? [], o.by_model ?? []);
  }
}

function merge(a: Share[], b: Share[]): Share[] {
  const m = new Map(a.map((s) => [s.name, { ...s }]));
  for (const s of b) {
    const t = m.get(s.name) ?? { name: s.name, tokens_in: 0, tokens_out: 0, cost_usd: 0 };
    t.tokens_in += s.tokens_in;
    t.tokens_out += s.tokens_out;
    t.cost_usd += s.cost_usd;
    m.set(s.name, t);
  }
  return [...m.values()].sort((x, y) => y.cost_usd - x.cost_usd || y.tokens_in + y.tokens_out - (x.tokens_in + x.tokens_out));
}
