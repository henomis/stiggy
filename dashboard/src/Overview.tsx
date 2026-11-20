// The front page: every namespace at a glance, what waits for a human, what
// just happened.

import { useState } from "react";

import * as api from "./api";
import { Empty, Section, ShareBars, StatusMark, Throughput } from "./Bits";
import { ago, duration, money, plural, shortId, tokens } from "./format";
import { href, useGraph } from "./hooks";
import { Review } from "./Review";
import { Trail } from "./Trail";

const LIVE: api.Status[] = ["running", "waiting"];

export function Overview({ data, refresh }: { data: api.Overview; refresh: () => void }) {
  const sum = (s: string) => data.namespaces.reduce((n, ns) => n + (ns.counts[s] ?? 0), 0);
  const spend = new api.UsageSum();
  for (const ns of data.namespaces) spend.add(ns.usage);
  const recent = data.namespaces
    .flatMap((ns) => ns.recent)
    .sort((a, b) => Date.parse(b.updated) - Date.parse(a.updated))
    .slice(0, 10);
  const failures = data.namespaces
    .flatMap((ns) => ns.failures)
    .sort((a, b) => Date.parse(b.updated) - Date.parse(a.updated))
    .slice(0, 6);

  if (!data.namespaces.length) {
    return (
      <Empty>
        <h2>No fleets on this NATS account yet</h2>
        <p>
          Start one with <code>stiggy run fleet.yaml</code>. Its namespace shows up here as soon as its engine is
          running.
        </p>
      </Empty>
    );
  }

  return (
    <>
      <Hero
        namespaces={data.namespaces.length}
        running={sum("running")}
        waiting={data.waiting.length}
        failed={sum("failed") + sum("quarantined")}
      />

      <div className="colony" role="list" aria-label="Namespaces">
        {data.namespaces.map((ns) => (
          <Band key={ns.name} ns={ns} />
        ))}
      </div>

      <div className="split">
        <div className="split-main">
          {data.waiting.length > 0 && (
            <Section title="Waiting for you" className="waiting" aside={<span className="count">{data.waiting.length}</span>}>
              <WaitList waiting={data.waiting} readOnly={data.read_only} refresh={refresh} />
            </Section>
          )}

          <Section title="Latest activity">
            {recent.length ? (
              <ul className="runs">
                {recent.map((s) => (
                  <RunRow key={s.namespace + s.exec_id} s={s} showNs={data.namespaces.length > 1} />
                ))}
              </ul>
            ) : (
              <p className="muted">No executions yet. Start one with <code>stiggy start &lt;flow&gt;</code>.</p>
            )}
          </Section>
        </div>

        <aside className="split-side">
          <Section title="Spend" aside={<span className="muted small">last {plural(data.namespaces.reduce((n, ns) => n + ns.sampled, 0), "execution")}</span>}>
            <div className="spend-totals">
              <div>
                <span className="big num">{money(spend.u.cost_usd)}</span>
                <span className="muted">cost</span>
              </div>
              <div>
                <span className="big num">{tokens(spend.u.tokens_in + spend.u.tokens_out)}</span>
                <span className="muted">tokens</span>
              </div>
              <div>
                <span className="big num">{tokens(spend.u.tool_calls ?? 0)}</span>
                <span className="muted">tool calls</span>
              </div>
            </div>
            {spend.u.by_agent && spend.u.by_agent.length > 0 && (
              <ShareBars shares={spend.u.by_agent.slice(0, 6)} by={spend.u.cost_usd > 0 ? "cost" : "tokens"} />
            )}
          </Section>

          <Section title="Failures">
            {failures.length ? (
              <ul className="failures">
                {failures.map((f) => (
                  <li key={f.namespace + f.exec_id}>
                    <a href={href.execution(f.namespace, f.exec_id)}>
                      <span className="fail-flow">{f.flow}</span>
                      <span className="muted small">{ago(f.updated)}</span>
                    </a>
                    <p className="fail-why">{f.error || f.reason || "No reason recorded."}</p>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="muted">Nothing failed recently.</p>
            )}
          </Section>

          <Section title="Agents on the bus" aside={<span className="count">{data.agents.length}</span>}>
            {data.agents_error && <p className="error">{data.agents_error}</p>}
            {data.agents.length ? (
              <ul className="agents">
                {data.agents.map((a) => (
                  <li key={a.owner + "/" + a.name}>
                    <span className="agent-name">{a.name}</span>
                    <span className="muted small">
                      {a.owner} on {a.framework}
                      {a.instances > 1 ? `, ${a.instances} replicas` : ""}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="muted">No agents exposed. Add <code>expose:</code> to an agent to reach it with <code>stiggy ask</code>.</p>
            )}
          </Section>
        </aside>
      </div>
    </>
  );
}

/** How many waits are shown in full; the rest are listed. */
const FULL_WAITS = 3;

function WaitList({ waiting, readOnly, refresh }: { waiting: api.Waiting[]; readOnly: boolean; refresh: () => void }) {
  const [all, setAll] = useState(false);
  const items = waiting.flatMap((w) => w.waits.map((wait) => ({ w, wait })));
  const full = items.slice(0, FULL_WAITS);
  const rest = items.slice(FULL_WAITS);
  const listed = all ? rest : rest.slice(0, 8);
  return (
    <>
      <div className="waits">
        {full.map(({ w, wait }) => (
          <div key={w.exec_id + wait.node} className="wait-card">
            <a className="wait-where" href={href.execution(w.namespace, w.exec_id)}>
              <span>{w.flow}</span>
              <span className="muted">
                {w.namespace}, <span className="mono">{shortId(w.exec_id)}</span>
              </span>
            </a>
            <Review ns={w.namespace} id={w.exec_id} wait={wait} readOnly={readOnly} compact onDone={refresh} />
          </div>
        ))}
      </div>
      {rest.length > 0 && (
        <>
          <ul className="more-waits">
            {listed.map(({ w, wait }) => (
              <li key={w.exec_id + wait.node}>
                <a href={href.execution(w.namespace, w.exec_id)}>
                  <span className="more-flow">
                    {w.flow} <span className="muted small">{wait.kind === "review" ? "to review" : "asks a question"} at {wait.node}</span>
                  </span>
                  <span className="muted small">
                    {w.namespace}, {ago(wait.asked_at ?? w.updated)}
                  </span>
                  <span className="more-text">{wait.text}</span>
                </a>
              </li>
            ))}
          </ul>
          {rest.length > listed.length && (
            <button type="button" className="link more-toggle" onClick={() => setAll(true)}>
              Show {plural(rest.length - listed.length, "more step")}
            </button>
          )}
        </>
      )}
    </>
  );
}

function Hero({ namespaces, running, waiting, failed }: { namespaces: number; running: number; waiting: number; failed: number }) {
  const where = namespaces === 1 ? "In the one namespace" : `Across ${namespaces} namespaces`;
  const parts: JSX.Element[] = [];
  if (running) parts.push(<Fig key="r" n={running} tone="running" word={running === 1 ? "execution is running" : "executions are running"} />);
  if (waiting) parts.push(<Fig key="w" n={waiting} tone="waiting" word={waiting === 1 ? "waits for you" : "wait for you"} />);
  if (failed) parts.push(<Fig key="f" n={failed} tone="failed" word={failed === 1 ? "has failed" : "have failed"} />);

  return (
    <h1 className="hero">
      {parts.length === 0 ? (
        <>{where}, everything is quiet.</>
      ) : (
        <>
          {where},{" "}
          {parts.map((p, i) => (
            <span key={i}>
              {p}
              {i < parts.length - 2 ? ", " : i === parts.length - 2 ? " and " : "."}
            </span>
          ))}
        </>
      )}
    </h1>
  );
}

function Fig({ n, tone, word }: { n: number; tone: string; word: string }) {
  return (
    <span className={`fig fig-${tone}`}>
      <span className="fig-n">{n}</span> {word}
    </span>
  );
}

function Band({ ns }: { ns: api.Namespace }) {
  const live = LIVE.reduce((n, s) => n + (ns.counts[s] ?? 0), 0);
  return (
    <a className={`band${ns.error ? " band-error" : ""}`} href={href.namespace(ns.name)} role="listitem">
      <div className="band-name">
        <span className="ns">{ns.name}</span>
        <span className="muted small">
          {plural(ns.flows.length, "flow")}, {plural(ns.total, "execution")}
        </span>
      </div>
      {ns.error ? (
        <p className="error band-body">{ns.error}</p>
      ) : (
        <>
          <div className="band-counts">
            {(["running", "waiting", "completed", "failed"] as const).map((s) => (
              <span key={s} className={`tally st-${s}${ns.counts[s] ? "" : " zero"}`} title={`${ns.counts[s] ?? 0} ${s}`}>
                <span className="tally-n num">{ns.counts[s] ?? 0}</span>
                <span className="tally-w">{s}</span>
              </span>
            ))}
          </div>
          <div className="band-chart">
            <Throughput buckets={ns.throughput} />
            <span className="muted small">started per hour, last 24h</span>
          </div>
          <div className="band-spend">
            <span className="num">{money(ns.usage.cost_usd)}</span>
            <span className="muted small">{tokens(ns.usage.tokens_in + ns.usage.tokens_out)} tokens</span>
          </div>
          {live > 0 && <span className="band-pulse" aria-hidden />}
        </>
      )}
    </a>
  );
}

export function RunRow({ s, showNs }: { s: api.Summary; showNs: boolean }) {
  const graph = useGraph(s.namespace, s.flow);
  const end = s.status === "running" || s.status === "waiting" ? Date.now() : Date.parse(s.updated);
  return (
    <li className={`run st-${s.status}`}>
      <a href={href.execution(s.namespace, s.exec_id)}>
        <span className="run-flow">
          <span className="run-name">{s.flow}</span>
          <span className="muted small">
            {showNs ? `${s.namespace}, ` : ""}
            <span className="mono">{shortId(s.exec_id)}</span>
          </span>
        </span>
        <span className="run-trail">{graph && <Trail graph={graph} nodes={s.nodes} mini />}</span>
        <StatusMark status={s.status} />
        <span className="run-time muted small num">
          {duration(end - Date.parse(s.created))}
          <br />
          {ago(s.updated)}
        </span>
        <span className="run-cost num small">{s.usage ? money(s.usage.cost_usd) : ""}</span>
      </a>
    </li>
  );
}
