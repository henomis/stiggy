// One namespace: its flows drawn as trails, and its executions.

import * as api from "./api";
import { Empty, Section } from "./Bits";
import { plural, statusWord } from "./format";
import { href, useGraph, usePoll } from "./hooks";
import { RunRow } from "./Overview";
import { Trail } from "./Trail";

const STATUSES = ["running", "waiting", "completed", "failed", "cancelled"];

export function NamespacePage({ ns, flow, status, overview }: { ns: string; flow?: string; status?: string; overview?: api.Overview }) {
  const list = usePoll(() => api.executions(ns, { flow, status, limit: 200 }), 4000, [ns, flow, status]);
  const view = overview?.namespaces.find((n) => n.name === ns);

  return (
    <>
      <nav className="crumbs" aria-label="Breadcrumb">
        <a href={href.overview()}>All namespaces</a>
      </nav>
      <h1 className="page-title">{ns}</h1>

      {view && view.flows.length > 0 && (
        <Section title="Flows">
          <div className="flows">
            {view.flows.map((f) => (
              <FlowCard key={f.name} ns={ns} f={f} active={f.name === flow} />
            ))}
          </div>
        </Section>
      )}

      <Section
        title={flow ? `Executions of ${flow}` : "Executions"}
        aside={
          <div className="filters" role="group" aria-label="Filter by status">
            <a className={`chip${!status ? " on" : ""}`} href={href.namespace(ns, { flow })}>
              Any status
            </a>
            {STATUSES.map((s) => (
              <a key={s} className={`chip st-${s}${status === s ? " on" : ""}`} href={href.namespace(ns, { flow, status: s })}>
                {statusWord(s)}
              </a>
            ))}
            {flow && (
              <a className="chip clear" href={href.namespace(ns, { status })}>
                All flows
              </a>
            )}
          </div>
        }
      >
        {list.error && <p className="error">{list.error}</p>}
        {!list.data ? (
          <p className="muted">Loading executions…</p>
        ) : list.data.length === 0 ? (
          <Empty>
            <p>No {status ? statusWord(status).toLowerCase() + " " : ""}executions{flow ? ` of ${flow}` : ""}.</p>
          </Empty>
        ) : (
          <ul className="runs">
            {list.data.map((s) => (
              <RunRow key={s.exec_id} s={s} showNs={false} />
            ))}
          </ul>
        )}
      </Section>
    </>
  );
}

function FlowCard({ ns, f, active }: { ns: string; f: api.FlowStats; active: boolean }) {
  const graph = useGraph(ns, f.name);
  const total = Object.values(f.counts).reduce((a, b) => a + b, 0);
  return (
    <a className={`flow-card${active ? " on" : ""}`} href={href.namespace(ns, { flow: active ? undefined : f.name })}>
      <div className="flow-head">
        <span className="flow-name">{f.name}</span>
        <span className="muted small">{plural(total, "recent execution")}</span>
      </div>
      {graph ? (
        <div className="flow-trail">
          <Trail graph={graph} />
        </div>
      ) : (
        <div className="flow-trail placeholder" />
      )}
      {graph?.description && <p className="muted small">{graph.description}</p>}
    </a>
  );
}
