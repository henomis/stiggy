// The settings panel of the selected block.

import { useState } from "react";

import type { Catalog } from "./api";
import { ATTACHMENT, ATTACHMENTS, BLOCK, TEAM_KINDS, newAttachment } from "./blocks";
import type { Attachment, AttachmentType, Block, Field, Problem, Project, Workflow } from "./project";
import { clean, identifier } from "./project";
import { available, type Value } from "./refs";
import {
  Check,
  FieldList,
  MappingList,
  NumberField,
  RefEditor,
  Row,
  SelectField,
  StringList,
  TextField,
  ValuePick,
} from "./widgets";

type Params = Record<string, unknown>;

interface Ctx {
  project: Project;
  wf: Workflow;
  catalog: Catalog;
  values: Value[];
  errors: (field: string) => string[];
  openCredentials: () => void;
}

interface PanelProps {
  project: Project;
  wf: Workflow;
  block: Block;
  catalog: Catalog;
  problems: Problem[];
  onChange: (b: Block) => void;
  onRenamePort: (from: string, to: string) => void;
  onDelete: () => void;
  openCredentials: () => void;
}

export function Panel({ project, wf, block, catalog, problems, onChange, onRenamePort, onDelete, openCredentials }: PanelProps) {
  const def = BLOCK[block.type];
  const params: Params = block.params ?? {};
  const set = (k: string, v: unknown) => onChange({ ...block, params: clean({ ...params, [k]: v }) });
  const setAll = (p: Params) => onChange({ ...block, params: clean(p) });

  const mine = problems.filter((p) => p.node === block.id);
  const errors = (field: string) =>
    mine.filter((p) => !p.attachment && (p.field === field || p.field?.startsWith(`${field}.`))).map((p) => p.msg);
  const general = mine.filter((p) => !p.attachment && !p.field);

  const ctx: Ctx = { project, wf, catalog, values: available(wf, block), errors, openCredentials };

  return (
    <div className="panel-body">
      <div className="panel-head">
        <span className="block-icon" style={{ background: `var(${def.color})` }}>
          {def.icon}
        </span>
        <div className="panel-title">
          <TextField value={block.name} onChange={(name) => name.trim() && onChange({ ...block, name: name.trim() })} />
          <div className="hint">{def.description}</div>
        </div>
        {!def.single && (
          <button type="button" className="danger" onClick={onDelete}>
            Delete
          </button>
        )}
      </div>

      {general.length > 0 && (
        <div className="problems-box">
          {general.map((p, i) => (
            <div key={i}>{p.msg}</div>
          ))}
        </div>
      )}

      {block.type === "start" && (
        <>
          <Row label="Inputs" hint="What the workflow is given when it starts." error={errors("inputs")}>
            <FieldList value={params.inputs} onChange={(v) => set("inputs", v)} add="Add input" />
          </Row>
        </>
      )}

      {block.type === "output" && (
        <Row label="Returns" hint="Leave empty to return the last block's answer." error={errors("fields")}>
          <MappingList value={params.fields} onChange={(v) => set("fields", v)} values={ctx.values} wf={wf} />
        </Row>
      )}

      {block.type === "agent" && <AgentFields ctx={ctx} params={params} set={set} />}

      {block.type === "router" && (
        <>
          <AgentFields ctx={ctx} params={params} set={set} />
          <Row label="Routes" hint="Each route is a way out of this block. Say when the agent should take it." error={errors("routes")}>
            <RouteList params={params} setAll={setAll} onRenamePort={onRenamePort} />
          </Row>
          <Check checked={params.ask_human} onChange={(v) => set("ask_human", v)} label="The agent may ask a person instead" />
        </>
      )}

      {block.type === "loop" && (
        <>
          <Row label="Loop over" hint="A list: usually a list field of an agent's structured answer." error={errors("over")}>
            <ValuePick
              value={params.over}
              onChange={(v) => set("over", v)}
              values={ctx.values.filter((v) => v.ref.root !== "item")}
              wf={wf}
              refsOnly
            />
          </Row>
          <Row label="At most at once" error={errors("max_parallel")}>
            <NumberField value={params.max_parallel} onChange={(v) => set("max_parallel", v)} placeholder="all" />
          </Row>
          <AgentFields ctx={ctx} params={params} set={set} taskHint="Use the current item from “+ value”." />
        </>
      )}

      {block.type === "condition" && <ConditionFields ctx={ctx} params={params} set={set} />}

      {block.type === "parallel" && (
        <Row label="Branches" hint="Connect each branch to one AI Agent, Agent team or Run workflow block, and those to a Merge." error={errors("branches")}>
          <PortList
            ports={(params.branches as string[]) ?? []}
            onChange={(v) => set("branches", v)}
            onRenamePort={onRenamePort}
            prefix="branch"
          />
        </Row>
      )}

      {block.type === "merge" && (
        <>
          <Row label="Continue when">
            <SelectField
              value={params.wait ?? "all"}
              empty="all branches are done"
              options={[
                { value: "all", label: "all branches are done" },
                { value: "first", label: "the first branch is done" },
                { value: "min", label: "all branches are done, some may fail" },
              ]}
              onChange={(v) => set("wait", v || "all")}
            />
          </Row>
          {params.wait === "min" && (
            <Row label="At least" hint="How many branches must succeed; 0 continues whatever happens." error={errors("min")}>
              <NumberField value={params.min} onChange={(v) => set("min", v)} placeholder="1" />
            </Row>
          )}
        </>
      )}

      {block.type === "approval" && (
        <>
          <Row label="Message" hint="What the person is asked." error={errors("message")}>
            <TextField value={params.message} onChange={(v) => set("message", v)} placeholder="Publish this article?" />
          </Row>
          <Row label="Wait up to" hint="e.g. 30m, 24h, 72h" error={errors("timeout")}>
            <TextField value={params.timeout} onChange={(v) => set("timeout", v)} placeholder="24h" />
          </Row>
          <div className="help">
            To answer, send the signal <code>{identifier(block.name) || "approval"}</code> to the run:
            <pre>{`stiggy signal <run> ${identifier(block.name) || "approval"} -payload '{"approved": true}'`}</pre>
          </div>
        </>
      )}

      {block.type === "subworkflow" && <SubworkflowFields ctx={ctx} block={block} params={params} set={set} />}

      {block.type === "team" && <TeamFields ctx={ctx} params={params} set={set} />}

      {def.ai && (
        <Attachments
          ctx={ctx}
          block={block}
          problems={mine.filter((p) => p.attachment)}
          onChange={(attachments) => onChange({ ...block, attachments })}
        />
      )}
    </div>
  );
}

function credentialOptions(p: Project) {
  return p.credentials.map((c) => ({ value: c.id, label: `${c.name} (${c.provider})` }));
}

function ModelFields({ ctx, params, set }: { ctx: Ctx; params: Params; set: (k: string, v: unknown) => void }) {
  return (
    <div className="model-row">
      <Row label="Model provider" error={ctx.errors("credential")}>
        <SelectField value={params.credential} options={credentialOptions(ctx.project)} onChange={(v) => set("credential", v)} />
        <button type="button" className="link small" onClick={ctx.openCredentials}>
          {ctx.project.credentials.length ? "Manage providers…" : "Add a provider…"}
        </button>
      </Row>
      <Row label="Model" error={ctx.errors("model")}>
        <TextField value={params.model} onChange={(v) => set("model", v)} placeholder="the provider's default" />
      </Row>
    </div>
  );
}

function AgentFields({
  ctx,
  params,
  set,
  taskHint,
}: {
  ctx: Ctx;
  params: Params;
  set: (k: string, v: unknown) => void;
  taskHint?: string;
}) {
  const structured = Array.isArray(params.output) && params.output.length > 0;
  const [more, setMore] = useState(false);

  return (
    <>
      <ModelFields ctx={ctx} params={params} set={set} />
      <Row label="Instructions" hint="Who the agent is and how it works." error={ctx.errors("instructions")}>
        <RefEditor
          value={params.instructions}
          onChange={(v) => set("instructions", v)}
          values={ctx.values}
          wf={ctx.wf}
          placeholder="You are a careful researcher…"
        />
      </Row>
      <Row label="Task" hint={taskHint ?? "What to do in this step. Insert values from earlier blocks with “+ value”."} error={ctx.errors("prompt")}>
        <RefEditor value={params.prompt} onChange={(v) => set("prompt", v)} values={ctx.values} wf={ctx.wf} placeholder="Summarise…" />
      </Row>
      <Check
        checked={structured}
        onChange={(on) => set("output", on ? [{ name: "result", type: "text" }] : [])}
        label="Answer with named fields (structured output)"
      />
      {structured && (
        <Row label="Answer fields" hint="Later blocks can use each field on its own." error={ctx.errors("output")}>
          <FieldList value={params.output} onChange={(v: Field[]) => set("output", v)} />
        </Row>
      )}
      <Check checked={params.remember} onChange={(v) => set("remember", v)} label="Remember the conversation within a run" />
      <button type="button" className="link more" onClick={() => setMore(!more)}>
        {more ? "− Fewer settings" : "+ More settings"}
      </button>
      {more && (
        <>
          <Row label="Temperature" hint="0 is focused, 1 is creative." error={ctx.errors("temperature")}>
            <NumberField value={params.temperature} onChange={(v) => set("temperature", v)} placeholder="default" />
          </Row>
          <Row label="Max tool steps" error={ctx.errors("max_steps")}>
            <NumberField value={params.max_steps} onChange={(v) => set("max_steps", v)} placeholder="default" />
          </Row>
        </>
      )}
    </>
  );
}

/** Port names that can be renamed and removed; renames move the connections. */
function PortList({
  ports,
  onChange,
  onRenamePort,
  prefix,
}: {
  ports: string[];
  onChange: (v: string[]) => void;
  onRenamePort: (from: string, to: string) => void;
  prefix: string;
}) {
  return (
    <div className="list">
      {ports.map((p, i) => (
        <div className="list-row" key={i}>
          <TextField
            value={p}
            sanitize={identifier}
            onChange={(v) => {
              if (!v || ports.includes(v)) return;
              onRenamePort(p, v);
              onChange(ports.map((x, j) => (j === i ? v : x)));
            }}
          />
          <button type="button" className="icon" aria-label="remove" onClick={() => onChange(ports.filter((_, j) => j !== i))}>
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        className="link"
        onClick={() => {
          let n = ports.length + 1;
          while (ports.includes(`${prefix}_${n}`)) n++;
          onChange([...ports, `${prefix}_${n}`]);
        }}
      >
        + Add {prefix}
      </button>
    </div>
  );
}

interface Route {
  port: string;
  when?: string;
}

function RouteList({ params, setAll, onRenamePort }: { params: Params; setAll: (p: Params) => void; onRenamePort: (from: string, to: string) => void }) {
  const routes = (Array.isArray(params.routes) ? params.routes : []) as Route[];
  const put = (rs: Route[]) => setAll({ ...params, routes: rs.map((r) => clean({ ...r })) });

  return (
    <div className="list">
      {routes.map((r, i) => (
        <div className="list-row route" key={i}>
          <TextField
            value={r.port}
            sanitize={identifier}
            onChange={(v) => {
              if (!v || routes.some((x) => x.port === v)) return;
              onRenamePort(r.port, v);
              put(routes.map((x, j) => (j === i ? { ...x, port: v } : x)));
            }}
          />
          <TextField value={r.when} placeholder="take it when…" onChange={(when) => put(routes.map((x, j) => (j === i ? { ...x, when } : x)))} />
          <button type="button" className="icon" aria-label="remove" onClick={() => put(routes.filter((_, j) => j !== i))}>
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        className="link"
        onClick={() => {
          let n = routes.length + 1;
          while (routes.some((r) => r.port === `route_${n}`)) n++;
          put([...routes, { port: `route_${n}`, when: "" }]);
        }}
      >
        + Add route
      </button>
    </div>
  );
}

const OPS: { value: string; label: string; unary?: boolean }[] = [
  { value: "equals", label: "is" },
  { value: "not_equals", label: "is not" },
  { value: "contains", label: "contains" },
  { value: "not_contains", label: "does not contain" },
  { value: "starts_with", label: "starts with" },
  { value: "ends_with", label: "ends with" },
  { value: "gt", label: ">" },
  { value: "gte", label: "≥" },
  { value: "lt", label: "<" },
  { value: "lte", label: "≤" },
  { value: "is_true", label: "is yes", unary: true },
  { value: "is_false", label: "is no", unary: true },
  { value: "is_empty", label: "is empty", unary: true },
  { value: "not_empty", label: "is not empty", unary: true },
];

interface Rule {
  left: string;
  op: string;
  value?: string;
}

function ConditionFields({ ctx, params, set }: { ctx: Ctx; params: Params; set: (k: string, v: unknown) => void }) {
  const rules = (Array.isArray(params.rules) ? params.rules : []) as Rule[];
  const put = (rs: Rule[]) => set("rules", rs.map((r) => clean({ ...r })));

  return (
    <>
      <Row label="Go to “true” when">
        <SelectField
          value={params.match ?? "all"}
          empty="all rules match"
          options={[
            { value: "all", label: "all rules match" },
            { value: "any", label: "any rule matches" },
          ]}
          onChange={(v) => set("match", v || "all")}
        />
      </Row>
      <Row label="Rules" error={ctx.errors("rules")}>
        <div className="list">
          {rules.map((r, i) => {
            const unary = OPS.find((o) => o.value === r.op)?.unary;
            return (
              <div className="rule" key={i}>
                <ValuePick value={r.left} onChange={(left) => put(rules.map((x, j) => (j === i ? { ...x, left } : x)))} values={ctx.values} wf={ctx.wf} refsOnly />
                <select value={r.op} onChange={(e) => put(rules.map((x, j) => (j === i ? { ...x, op: e.target.value } : x)))}>
                  {OPS.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
                {!unary && (
                  <ValuePick value={r.value} onChange={(value) => put(rules.map((x, j) => (j === i ? { ...x, value } : x)))} values={ctx.values} wf={ctx.wf} />
                )}
                <button type="button" className="icon" aria-label="remove" onClick={() => put(rules.filter((_, j) => j !== i))}>
                  ×
                </button>
                {ctx.errors(`rules.${i}`).map((e, k) => (
                  <div className="error" key={k}>
                    {e}
                  </div>
                ))}
              </div>
            );
          })}
          <button type="button" className="link" onClick={() => put([...rules, { left: "", op: "equals", value: "" }])}>
            + Add rule
          </button>
        </div>
      </Row>
    </>
  );
}

function SubworkflowFields({ ctx, block, params, set }: { ctx: Ctx; block: Block; params: Params; set: (k: string, v: unknown) => void }) {
  const others = ctx.project.workflows.filter((w) => w.id !== ctx.wf.id);
  const target = ctx.project.workflows.find((w) => w.id === params.workflow);
  const start = target?.nodes.find((n) => n.type === "start");
  const inputs = ((start?.params?.inputs as Field[]) ?? []).map((f) => f.name);

  return (
    <>
      <Row label="Workflow" error={ctx.errors("workflow")}>
        <SelectField value={params.workflow} options={others.map((w) => ({ value: w.id, label: w.name }))} onChange={(v) => set("workflow", v)} />
      </Row>
      {target && (
        <Row label="Its inputs" hint={inputs.length ? undefined : `${target.name} has no inputs.`} error={ctx.errors("inputs")}>
          <MappingList value={params.inputs} onChange={(v) => set("inputs", v)} values={ctx.values} wf={ctx.wf} fixedNames={inputs} key={block.id} />
        </Row>
      )}
    </>
  );
}

interface Member {
  name: string;
  credential?: string;
  model?: string;
  instructions?: string;
}

interface TeamTask {
  member: string;
  description: string;
  expected_output?: string;
  async?: boolean;
}

function TeamFields({ ctx, params, set }: { ctx: Ctx; params: Params; set: (k: string, v: unknown) => void }) {
  const kind = String(params.kind ?? "");
  const info = TEAM_KINDS[kind];
  const members = (Array.isArray(params.members) ? params.members : []) as Member[];
  const names = members.map((m) => m.name).filter(Boolean);
  const memberOptions = names.map((n) => ({ value: n, label: n }));
  const putMembers = (ms: Member[]) => set("members", ms.map((m) => clean({ ...m })));
  const tasks = (Array.isArray(params.tasks) ? params.tasks : []) as TeamTask[];
  const putTasks = (ts: TeamTask[]) => set("tasks", ts.map((t) => clean({ ...t })));

  return (
    <>
      <Row label="Kind of team" hint={info?.description} error={ctx.errors("kind")}>
        <SelectField
          value={kind}
          options={Object.entries(TEAM_KINDS).map(([value, k]) => ({ value, label: k.label }))}
          onChange={(v) => set("kind", v)}
        />
      </Row>

      {kind !== "crew" && (
        <Row label="Task" hint="What the team works on." error={ctx.errors("task")}>
          <RefEditor value={params.task} onChange={(v) => set("task", v)} values={ctx.values} wf={ctx.wf} />
        </Row>
      )}

      <Row label="Members" error={ctx.errors("members")}>
        <div className="members">
          {members.map((m, i) => (
            <div className="member" key={i}>
              <div className="inline">
                <TextField value={m.name} placeholder="name" onChange={(name) => putMembers(members.map((x, j) => (j === i ? { ...x, name } : x)))} />
                <button type="button" className="icon" aria-label="remove" onClick={() => putMembers(members.filter((_, j) => j !== i))}>
                  ×
                </button>
              </div>
              <div className="inline">
                <SelectField
                  value={m.credential}
                  options={credentialOptions(ctx.project)}
                  onChange={(credential) => putMembers(members.map((x, j) => (j === i ? { ...x, credential } : x)))}
                />
                <TextField value={m.model} placeholder="model" onChange={(model) => putMembers(members.map((x, j) => (j === i ? { ...x, model } : x)))} />
              </div>
              <TextField
                value={m.instructions}
                placeholder="instructions"
                onChange={(instructions) => putMembers(members.map((x, j) => (j === i ? { ...x, instructions } : x)))}
              />
            </div>
          ))}
          <button
            type="button"
            className="link"
            onClick={() => putMembers([...members, { name: `Member ${members.length + 1}`, credential: ctx.project.credentials[0]?.id }])}
          >
            + Add member
          </button>
        </div>
      </Row>

      {info?.roles.map((role) => (
        <Row key={role} label={role[0].toUpperCase() + role.slice(1)} error={ctx.errors(role)}>
          <SelectField value={params[role]} options={memberOptions} onChange={(v) => set(role, v)} />
        </Row>
      ))}

      {(kind === "debate" || kind === "evaluator_optimizer") && (
        <Row label="Rounds" error={ctx.errors("rounds")}>
          <NumberField value={params.rounds} onChange={(v) => set("rounds", v)} placeholder="default" />
        </Row>
      )}

      {kind === "plan_execute" && (
        <Row label="Parts at once" error={ctx.errors("max_parallel")}>
          <NumberField value={params.max_parallel} onChange={(v) => set("max_parallel", v)} placeholder="default" />
        </Row>
      )}

      {kind === "crew" && (
        <>
          <Row label="Tasks" hint="Done in order; each sees the results of the earlier ones." error={ctx.errors("tasks")}>
            <div className="members">
              {tasks.map((t, i) => (
                <div className="member" key={i}>
                  <div className="inline">
                    <SelectField value={t.member} options={memberOptions} empty="Who does it?" onChange={(member) => putTasks(tasks.map((x, j) => (j === i ? { ...x, member } : x)))} />
                    <button type="button" className="icon" aria-label="remove" onClick={() => putTasks(tasks.filter((_, j) => j !== i))}>
                      ×
                    </button>
                  </div>
                  <RefEditor
                    value={t.description}
                    onChange={(description) => putTasks(tasks.map((x, j) => (j === i ? { ...x, description } : x)))}
                    values={ctx.values}
                    wf={ctx.wf}
                    placeholder="What to do"
                  />
                  <TextField
                    value={t.expected_output}
                    placeholder="expected result (optional)"
                    onChange={(expected_output) => putTasks(tasks.map((x, j) => (j === i ? { ...x, expected_output } : x)))}
                  />
                  <Check checked={t.async} onChange={(async) => putTasks(tasks.map((x, j) => (j === i ? { ...x, async } : x)))} label="Run alongside the next task" />
                  {ctx.errors(`tasks.${i}`).map((e, k) => (
                    <div className="error" key={k}>
                      {e}
                    </div>
                  ))}
                </div>
              ))}
              <button type="button" className="link" onClick={() => putTasks([...tasks, { member: names[0] ?? "", description: "" }])}>
                + Add task
              </button>
            </div>
          </Row>
          <Row label="Manager (optional)" hint="A member who assigns the tasks instead of doing them in order." error={ctx.errors("manager")}>
            <SelectField value={params.manager} options={memberOptions} empty="No manager" onChange={(v) => set("manager", v)} />
          </Row>
        </>
      )}
    </>
  );
}

function Attachments({
  ctx,
  block,
  problems,
  onChange,
}: {
  ctx: Ctx;
  block: Block;
  problems: Problem[];
  onChange: (a: Attachment[]) => void;
}) {
  const list = block.attachments ?? [];
  const [open, setOpen] = useState<string | null>(null);

  return (
    <div className="attachments">
      <h4>Tools &amp; knowledge</h4>
      <p className="hint">Drag them from the palette onto the block, or add one here.</p>
      {list.map((a) => {
        const def = ATTACHMENT[a.type];
        const errs = problems.filter((p) => p.attachment === a.id);
        return (
          <div className={`attachment${errs.length ? " has-error" : ""}`} key={a.id}>
            <div className="attachment-head" onClick={() => setOpen(open === a.id ? null : a.id)}>
              <span>{def.icon}</span>
              <strong>{a.name}</strong>
              <span className="hint">{def.summary(a)}</span>
              <button
                type="button"
                className="icon"
                aria-label="remove"
                onClick={(e) => {
                  e.stopPropagation();
                  onChange(list.filter((x) => x.id !== a.id));
                }}
              >
                ×
              </button>
            </div>
            {errs.map((p, i) => (
              <div className="error" key={i}>
                {p.msg}
              </div>
            ))}
            {open === a.id && (
              <AttachmentForm
                ctx={ctx}
                a={a}
                onChange={(next) => onChange(list.map((x) => (x.id === a.id ? next : x)))}
              />
            )}
          </div>
        );
      })}
      <select
        value=""
        onChange={(e) => {
          if (!e.target.value) return;
          const a = newAttachment(e.target.value as AttachmentType);
          onChange([...list, a]);
          setOpen(a.id);
        }}
      >
        <option value="">+ Add tool or knowledge…</option>
        {ATTACHMENTS.map((d) => (
          <option key={d.type} value={d.type}>
            {d.icon} {d.label}
          </option>
        ))}
      </select>
    </div>
  );
}

const STORE_FIELDS: Record<string, { key: string; label: string; number?: boolean; placeholder?: string }[]> = {
  qdrant: [
    { key: "host", label: "Host", placeholder: "localhost" },
    { key: "port", label: "Port", number: true, placeholder: "6334" },
    { key: "collection", label: "Collection" },
    { key: "vector_size", label: "Vector size", number: true, placeholder: "768" },
  ],
  weaviate: [
    { key: "host", label: "Host", placeholder: "localhost:8080" },
    { key: "scheme", label: "Scheme", placeholder: "http" },
    { key: "class", label: "Class" },
    { key: "vector_size", label: "Vector size", number: true },
  ],
  psql: [
    { key: "dsn", label: "Connection (DSN)", placeholder: "postgres://…" },
    { key: "collection", label: "Collection" },
    { key: "vector_size", label: "Vector size", number: true },
  ],
};

export function AttachmentForm({ ctx, a, onChange }: { ctx: Ctx; a: Attachment; onChange: (a: Attachment) => void }) {
  const p: Params = a.params ?? {};
  const set = (k: string, v: unknown) => onChange({ ...a, params: clean({ ...p, [k]: v }) });

  return (
    <div className="attachment-form">
      <Row label="Name">
        <TextField value={a.name} onChange={(name) => name && onChange({ ...a, name })} />
      </Row>

      {a.type === "knowledge" && (
        <>
          <Row label="Description" hint="Tells the agent what it can find here.">
            <TextField value={p.description} onChange={(v) => set("description", v)} />
          </Row>
          <Row label="Documents" hint="A file, folder or pattern such as docs/*.md, loaded when the fleet starts.">
            <TextField value={p.documents} onChange={(v) => set("documents", v)} placeholder="docs/*.md" />
          </Row>
          <div className="inline">
            <Row label="Chunk size">
              <NumberField value={p.chunk_size} onChange={(v) => set("chunk_size", v)} placeholder="default" />
            </Row>
            <Row label="Results per search">
              <NumberField value={p.top_k} onChange={(v) => set("top_k", v)} placeholder="default" />
            </Row>
          </div>
          <Row label="Embedding provider">
            <SelectField
              value={p.embedding_credential}
              options={ctx.project.credentials
                .filter((c) => ctx.catalog.embedder_providers.includes(c.provider))
                .map((c) => ({ value: c.id, label: `${c.name} (${c.provider})` }))}
              onChange={(v) => set("embedding_credential", v)}
            />
          </Row>
          <Row label="Embedding model">
            <TextField value={p.embedding_model} onChange={(v) => set("embedding_model", v)} placeholder="nomic-embed-text, text-embedding-3-small…" />
          </Row>
          <Row label="Stored in">
            <SelectField
              value={p.store}
              options={ctx.catalog.vectorstore_types.map((t) => ({ value: t, label: t }))}
              onChange={(v) => onChange({ ...a, params: clean({ ...p, store: v, store_options: {} }) })}
            />
          </Row>
          {(STORE_FIELDS[String(p.store)] ?? []).map((f) => {
            const opts = (p.store_options ?? {}) as Params;
            const put = (v: unknown) => set("store_options", clean({ ...opts, [f.key]: v }));
            return (
              <Row label={f.label} key={f.key}>
                {f.number ? (
                  <NumberField value={opts[f.key]} onChange={put} placeholder={f.placeholder} />
                ) : (
                  <TextField value={opts[f.key]} onChange={put} placeholder={f.placeholder} />
                )}
              </Row>
            );
          })}
        </>
      )}

      {a.type === "files" && (
        <>
          <Row label="Folder">
            <TextField value={p.working_dir} onChange={(v) => set("working_dir", v)} placeholder="/srv/data" />
          </Row>
          <div className="checks">
            <Check checked={p.read} onChange={(v) => set("read", v)} label="Read" />
            <Check checked={p.search} onChange={(v) => set("search", v)} label="Search" />
            <Check checked={p.write} onChange={(v) => set("write", v)} label="Write" />
            <Check checked={p.edit} onChange={(v) => set("edit", v)} label="Edit" />
          </div>
        </>
      )}

      {a.type === "shell" && (
        <>
          <Row label="Folder">
            <TextField value={p.working_dir} onChange={(v) => set("working_dir", v)} />
          </Row>
          <Row label="Allowed commands" hint="Empty allows any command.">
            <StringList value={p.allowlist} onChange={(v) => set("allowlist", v.filter(Boolean))} placeholder="ls" />
          </Row>
          <Check checked={p.safe_mode} onChange={(v) => set("safe_mode", v)} label="Safe mode (refuse dangerous commands)" />
          <Row label="Time limit" hint="e.g. 30s">
            <TextField value={p.timeout} onChange={(v) => set("timeout", v)} />
          </Row>
        </>
      )}

      {a.type === "mcp" && (
        <>
          <Row label="Command" hint="Starts the server; leave empty to use a URL.">
            <TextField value={p.command} onChange={(v) => set("command", v)} placeholder="npx" />
          </Row>
          <Row label="Arguments">
            <StringList value={p.args} onChange={(v) => set("args", v.filter(Boolean))} />
          </Row>
          <Row label="URL" hint="A running server, instead of a command.">
            <TextField value={p.url} onChange={(v) => set("url", v)} placeholder="http://localhost:3000/mcp" />
          </Row>
          <Row label="Only these tools" hint="Empty allows all of them.">
            <StringList value={p.allow} onChange={(v) => set("allow", v.filter(Boolean))} />
          </Row>
        </>
      )}

      {a.type === "kv" && (
        <Row label="Bucket" hint="Where the values are kept.">
          <TextField value={p.bucket} onChange={(v) => set("bucket", v)} />
        </Row>
      )}

      {a.type === "skills" && (
        <Row label="Folder" hint="Where the skills are.">
          <TextField value={p.root} onChange={(v) => set("root", v)} />
        </Row>
      )}
    </div>
  );
}
