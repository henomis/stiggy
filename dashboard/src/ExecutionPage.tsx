// One execution: where it is in its flow, what it spent, what its agents are
// doing, and what waits for a human.

import { useState } from "react";

import * as api from "./api";
import { Fields, Meter, Prose, Section, ShareBars, StatusMark } from "./Bits";
import { ago, duration, money, plural, preview, tokens } from "./format";
import { href, useNow, usePoll } from "./hooks";
import { LiveSteps } from "./LiveSteps";
import { Review } from "./Review";
import { StepConfig } from "./StepConfig";
import { Story } from "./Story";
import { Timeline } from "./Timeline";
import { TimeTravel } from "./TimeTravel";
import { Trail } from "./Trail";

const OPEN = new Set(["running", "waiting"]);

export function ExecutionPage({ ns, id, readOnly }: { ns: string; id: string; readOnly: boolean }) {
  const ex = usePoll(() => api.execution(ns, id), 2500, [ns, id]);
  const hist = usePoll(() => api.history(ns, id), 2500, [ns, id]);
  const now = useNow();
  const [selected, setSelected] = useState<string>();
  const [showInput, setShowInput] = useState(false);
  // focus narrows the story to one step; set by clicking a step.
  const [focus, setFocus] = useState<string>();
  const pick = (n: string) => {
    setSelected(n);
    setFocus(n);
  };
  // seq is the moment being replayed; undefined: now.
  const [seq, setSeq] = useState<number>();
  const past = usePoll(() => (seq ? api.execution(ns, id, seq) : Promise.resolve(undefined)), 60_000, [ns, id, seq]);

  const refresh = () => {
    ex.refresh();
    hist.refresh();
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

  const live = ex.data;
  if (!live) return <p className="muted pad">Loading the execution…</p>;

  // While replaying, the trail, state and spending are those of the moment.
  const e = (seq !== undefined && past.data) || live;
  const open = OPEN.has(live.status);
  const ran = (open ? now : Date.parse(live.updated)) - Date.parse(live.created);
  const node = selected ?? live.waits[0]?.node ?? live.failed_node ?? live.last_node;
  const h = hist.data;

  return (
    <>
      <Crumbs ns={ns} flow={e.flow} />
      <header className="exec-head">
        <div>
          <h1 className="page-title">
            {live.flow} <StatusMark status={live.status} />
          </h1>
          {live.input.length > 0 && (
            <div className="exec-input">
              <p>
                <span className="exec-input-label">Started with</span>{" "}
                <button
                  type="button"
                  className="exec-input-value"
                  aria-expanded={showInput}
                  title={showInput ? "Hide the whole input" : "Show the whole input"}
                  onClick={() => setShowInput(!showInput)}
                >
                  {preview(live.input)}
                </button>
              </p>
              {showInput && (
                <div className="exec-input-all">
                  <Fields fields={live.input} empty="" />
                </div>
              )}
            </div>
          )}
          <p className="exec-meta">
            <span className="mono">{e.exec_id}</span>
            <span>started {ago(e.created, now)}</span>
            <span>{open ? "running for" : "ran for"} {duration(ran)}</span>
            <span>{plural(live.steps, "step")}</span>
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

      {(live.status === "failed" || live.status === "cancelled") && (live.error || live.reason) && (
        <div className={`banner b-${live.status}`} role="status">
          <strong>{live.status === "failed" ? `Failed${live.failed_node ? ` at ${live.failed_node}` : ""}` : "Cancelled"}</strong>
          <Prose text={[live.reason, live.error].filter(Boolean).join(": ")} lines={4} />
        </div>
      )}

      {live.waits.length > 0 && (
        <Section title={live.waits.length === 1 ? "Waiting for you" : `${live.waits.length} steps wait for you`} className="waiting">
          <div className="waits wide">
            {live.waits.map((w) => (
              <Review key={w.node} ns={ns} id={id} wait={w} readOnly={readOnly} onDone={refresh} />
            ))}
          </div>
        </Section>
      )}

      <div className={`trail-stage${seq !== undefined ? " replay" : ""}`}>
        <div className="trail-canvas">
          {e.graph ? (
            <Trail graph={e.graph} nodes={e.nodes} selected={node} onSelect={pick} />
          ) : (
            <p className="muted">The flow definition of this execution is not registered any more.</p>
          )}
        </div>
        <NodeDetail
          e={e}
          node={node}
          config={node ? h?.steps[node]?.config : undefined}
          tl={h?.timeline}
          now={seq !== undefined ? Date.parse(h?.moments.find((m) => m.seq === seq)?.time ?? live.updated) : now}
        />
        {h && (
          <div className="trail-foot">
            <TimeTravel moments={h.moments} seq={seq} onChange={setSeq} />
          </div>
        )}
      </div>

      {h && h.story.length > 0 && (
        <Section title="What happened" aside={<span className="muted small">{plural(h.story.length, "entry", "entries")}</span>}>
          <Story
            ns={ns}
            id={id}
            story={h.story}
            until={seq !== undefined ? Date.parse(h.moments.find((m) => m.seq === seq)?.time ?? live.updated) : undefined}
            selected={node}
            focus={focus}
            onSelect={pick}
            onClearFocus={() => setFocus(undefined)}
          />
        </Section>
      )}


      <div className="exec-grid">
        <Section title={seq !== undefined ? "Spend at this moment" : "Spend"}>
          <UsageView u={e.usage} budget={e.budget} steps={e.steps} maxSteps={e.max_steps} />
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
        {h ? <Timeline tl={h.timeline} now={now} selected={node} onSelect={pick} /> : <p className="muted">Loading…</p>}
      </Section>

      <div className="exec-grid">
        {e.channels.length > 0 && (
          <Section title={seq !== undefined ? "Shared state at this moment" : "Shared state"}>
            <Fields fields={e.channels} empty="" />
          </Section>
        )}
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

function NodeDetail({ e, node, config, tl, now }: { e: api.Execution; node?: string; config?: api.StepConfig; tl?: api.Timeline; now: number }) {
  const gn = e.graph?.nodes.find((n) => n.id === node);
  if (!node || !gn) {
    return (
      <aside className="node-detail">
        <p className="muted">Select a step to see how it is set up and what it did.</p>
        <Legend />
      </aside>
    );
  }
  const st = e.nodes?.[node] ?? { state: "idle" };
  // While replaying, now is the moment: later attempts have not happened.
  const spans = (tl?.spans.filter((s) => s.node === node) ?? [])
    .filter((s) => Date.parse(s.start) <= now)
    .map((s) => (s.end && Date.parse(s.end) > now ? { ...s, end: undefined } : s));
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
            {spans.some((s) => (s.attempt ?? 1) > 1) && (
              <>
                <dt>Retries</dt>
                <dd className="num">{spans.filter((s) => (s.attempt ?? 1) > 1).length}</dd>
              </>
            )}
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
      {config && <StepConfig c={config} />}
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

function UsageView({ u, budget, steps, maxSteps }: { u?: api.Usage; budget: api.Execution["budget"]; steps: number; maxSteps?: number }) {
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
      {(budget.length > 0 || !!maxSteps) && (
        <>
          <h3 className="sub">Limits</h3>
          {!!maxSteps && <Meter label="steps" used={steps} limit={maxSteps} format={(n) => String(n)} />}
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
