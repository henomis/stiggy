// One execution: where it is in its flow, what it spent, what its agents are
// doing, and what waits for a human.

import { useState } from "react";

import * as api from "./api";
import { Fields, Meter, Prose, Section, ShareBars, StatusMark } from "./Bits";
import { ago, duration, money, plural, tokens } from "./format";
import { href, useNow, usePoll } from "./hooks";
import { LiveSteps } from "./LiveSteps";
import { Review } from "./Review";
import { Timeline } from "./Timeline";
import { Trail } from "./Trail";

const OPEN = new Set(["running", "waiting"]);

export function ExecutionPage({ ns, id, readOnly }: { ns: string; id: string; readOnly: boolean }) {
  const ex = usePoll(() => api.execution(ns, id), 2500, [ns, id]);
  const tl = usePoll(() => api.timeline(ns, id), 2500, [ns, id]);
  const now = useNow();
  const [selected, setSelected] = useState<string>();

  const refresh = () => {
    ex.refresh();
    tl.refresh();
  };

  if (ex.error && !ex.data) {
    return (
      <>
        <Crumbs ns={ns} />
        <div className="empty">
          <h2>This execution could not be read</h2>
          <p className="error">{ex.error}</p>
        </div>
      </>
    );
  }

  const e = ex.data;
  if (!e) return <p className="muted pad">Loading the execution…</p>;

  const open = OPEN.has(e.status);
  const ran = (open ? now : Date.parse(e.updated)) - Date.parse(e.created);
  const node = selected ?? e.waits[0]?.node ?? e.failed_node;

  return (
    <>
      <Crumbs ns={ns} flow={e.flow} />
      <header className="exec-head">
        <div>
          <h1 className="page-title">
            {e.flow} <StatusMark status={e.status} />
          </h1>
          <p className="exec-meta">
            <span className="mono">{e.exec_id}</span>
            <span>started {ago(e.created, now)}</span>
            <span>{open ? "running for" : "ran for"} {duration(ran)}</span>
            <span>{plural(e.steps, "step")}</span>
            {e.parent && (
              <a href={href.execution(ns, e.parent)}>
                child of <span className="mono">{e.parent}</span>
              </a>
            )}
            {e.forked_from && (
              <a href={href.execution(ns, e.forked_from)}>
                forked from <span className="mono">{e.forked_from}</span>
              </a>
            )}
          </p>
        </div>
        {open && !readOnly && <CancelButton ns={ns} id={id} onDone={refresh} />}
      </header>

      {(e.status === "failed" || e.status === "cancelled") && (e.error || e.reason) && (
        <div className={`banner b-${e.status}`} role="status">
          <strong>{e.status === "failed" ? `Failed${e.failed_node ? ` at ${e.failed_node}` : ""}` : "Cancelled"}</strong>
          <Prose text={[e.reason, e.error].filter(Boolean).join(": ")} lines={4} />
        </div>
      )}

      {e.waits.length > 0 && (
        <Section title={e.waits.length === 1 ? "Waiting for you" : `${e.waits.length} steps wait for you`} className="waiting">
          <div className="waits wide">
            {e.waits.map((w) => (
              <Review key={w.node} ns={ns} id={id} wait={w} readOnly={readOnly} onDone={refresh} />
            ))}
          </div>
        </Section>
      )}

      <div className="trail-stage">
        <div className="trail-canvas">
          {e.graph ? (
            <Trail graph={e.graph} nodes={e.nodes} selected={node} onSelect={setSelected} />
          ) : (
            <p className="muted">The flow definition of this execution is not registered any more.</p>
          )}
        </div>
        <NodeDetail e={e} node={node} tl={tl.data} now={now} />
      </div>

      <div className="exec-grid">
        <Section title="Spend">
          <UsageView u={e.usage} budget={e.budget} />
        </Section>
        {open || e.status === "running" ? (
          <Section title="Live">
            <LiveSteps ns={ns} id={id} live={open} />
          </Section>
        ) : (
          <Section title="Output">
            <Fields fields={e.output} empty="This execution returned nothing." />
          </Section>
        )}
      </div>

      <Section title="Timeline">
        {tl.data ? <Timeline tl={tl.data} now={now} selected={node} onSelect={setSelected} /> : <p className="muted">Loading…</p>}
      </Section>

      <div className="exec-grid">
        <Section title="Input">
          <Fields fields={e.input} empty="Started without input." />
        </Section>
        {open && (
          <Section title="Output">
            <p className="muted">The output appears when the execution completes.</p>
          </Section>
        )}
        {e.children.length > 0 && (
          <Section title="Child executions">
            <ul className="children">
              {e.children.map((c) => (
                <li key={c.exec_id}>
                  <a href={href.execution(ns, c.exec_id)}>
                    {c.flow} <span className="muted small">from {c.node}</span>
                  </a>
                </li>
              ))}
            </ul>
          </Section>
        )}
      </div>
    </>
  );
}

function Crumbs({ ns, flow }: { ns: string; flow?: string }) {
  return (
    <nav className="crumbs" aria-label="Breadcrumb">
      <a href={href.overview()}>All namespaces</a>
      <a href={href.namespace(ns)}>{ns}</a>
      {flow && <a href={href.namespace(ns, { flow })}>{flow}</a>}
    </nav>
  );
}

function NodeDetail({ e, node, tl, now }: { e: api.Execution; node?: string; tl?: api.Timeline; now: number }) {
  const gn = e.graph?.nodes.find((n) => n.id === node);
  if (!node || !gn) {
    return (
      <aside className="node-detail">
        <p className="muted">Select a step to see its attempts.</p>
        <Legend />
      </aside>
    );
  }
  const st = e.nodes?.[node] ?? { state: "idle" };
  const spans = tl?.spans.filter((s) => s.node === node) ?? [];
  const err = e.errors.find((x) => x.node === node);
  const spent = spans.reduce((a, s) => ({ t: a.t + (s.tokens ?? 0), c: a.c + (s.cost_usd ?? 0) }), { t: 0, c: 0 });

  return (
    <aside className="node-detail">
      <h3>{node}</h3>
      <p className="muted">
        {gn.role ? `${gn.role} ${gn.runs}` : gn.detail ?? gn.type}
        {gn.human ? ", stops for a human" : ""}
      </p>
      <dl className="kv">
        <dt>State</dt>
        <dd className={`s-text s-${st.state}`}>{st.state === "idle" ? "not reached" : st.state}</dd>
        {(st.visits ?? 0) > 0 && (
          <>
            <dt>Visits</dt>
            <dd className="num">{st.visits}</dd>
          </>
        )}
        {spans.length > 0 && (
          <>
            <dt>Attempts</dt>
            <dd className="num">{spans.filter((s) => s.outcome !== "human").length}</dd>
            <dt>Time</dt>
            <dd className="num">
              {duration(spans.reduce((n, s) => n + ((s.end ? Date.parse(s.end) : now) - Date.parse(s.start)), 0))}
            </dd>
          </>
        )}
        {spent.t > 0 && (
          <>
            <dt>Spent</dt>
            <dd className="num">
              {tokens(spent.t)} tokens{spent.c ? `, ${money(spent.c)}` : ""}
            </dd>
          </>
        )}
        {st.total ? (
          <>
            <dt>Items</dt>
            <dd className="num">
              {st.done ?? 0} of {st.total}
            </dd>
          </>
        ) : null}
      </dl>
      {st.child && (
        <a className="link" href={href.execution(e.namespace, st.child)}>
          Open the child execution
        </a>
      )}
      {(err || st.error) && (
        <div className="node-error">
          <Prose text={err?.error ?? st.error ?? ""} lines={5} />
        </div>
      )}
    </aside>
  );
}

function Legend() {
  return (
    <ul className="legend">
      <li className="s-done">walked</li>
      <li className="s-running">working now</li>
      <li className="s-waiting">waiting for you</li>
      <li className="s-failed">failed</li>
      <li className="s-idle">not reached</li>
    </ul>
  );
}

function UsageView({ u, budget }: { u?: api.Usage; budget: api.Execution["budget"] }) {
  if (!u) return null;
  const tiles: [string, string][] = [
    ["cost", money(u.cost_usd)],
    ["tokens in", tokens(u.tokens_in)],
    ["tokens out", tokens(u.tokens_out)],
    ["model calls", tokens(u.llm_calls ?? 0)],
    ["tool calls", tokens(u.tool_calls ?? 0)],
  ];
  if (u.tool_errors) tiles.push(["tool errors", tokens(u.tool_errors)]);
  const by = u.cost_usd > 0 ? "cost" : "tokens";

  return (
    <div className="usage">
      <div className="tiles">
        {tiles.map(([k, v]) => (
          <div key={k} className={`tile${k === "tool errors" ? " warn" : ""}`}>
            <span className="big num">{v}</span>
            <span className="muted small">{k}</span>
          </div>
        ))}
      </div>
      {u.by_agent && u.by_agent.length > 0 && (
        <>
          <h3 className="sub">By agent</h3>
          <ShareBars shares={u.by_agent} by={by} />
        </>
      )}
      {u.by_model && u.by_model.length > 0 && (
        <>
          <h3 className="sub">By model</h3>
          <ShareBars shares={u.by_model} by={by} />
        </>
      )}
      {budget.length > 0 && (
        <>
          <h3 className="sub">Budget</h3>
          {budget.map((b) => (
            <Meter
              key={b.counter}
              label={b.counter.replace(/_/g, " ")}
              used={b.used}
              limit={b.limit}
              format={b.counter === "cost_usd" ? money : tokens}
            />
          ))}
        </>
      )}
    </div>
  );
}

function CancelButton({ ns, id, onDone }: { ns: string; id: string; onDone: () => void }) {
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  if (!confirm) {
    return (
      <button type="button" className="btn danger-ghost" onClick={() => setConfirm(true)}>
        Cancel execution
      </button>
    );
  }
  return (
    <div className="confirm">
      <span>Stop this execution for good?</span>
      <button
        type="button"
        className="btn danger"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await api.cancel(ns, id);
            setConfirm(false);
            onDone();
          } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        Cancel execution
      </button>
      <button type="button" className="btn ghost" onClick={() => setConfirm(false)}>
        Keep running
      </button>
      {error && <span className="error">{error}</span>}
    </div>
  );
}
