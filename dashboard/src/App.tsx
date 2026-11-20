import * as api from "./api";
import { ago } from "./format";
import { href, useLive, useNow, usePoll, useRoute } from "./hooks";
import { ExecutionPage } from "./ExecutionPage";
import { NamespacePage } from "./NamespacePage";
import { Overview } from "./Overview";

export function App() {
  const route = useRoute();
  const ov = usePoll(api.overview, 5000, []);
  const live = useLive(() => ov.refresh());
  const now = useNow(5000);

  return (
    <div className="app">
      <header className="top">
        <a className="brand" href={href.overview()} aria-label="stiggy dashboard, all namespaces">
          <svg viewBox="0 0 32 32" width="26" height="26" aria-hidden>
            <path d="M4 26 C10 22 12 10 18 12 S26 6 28 4" className="brand-trail" />
            <circle cx="28" cy="4" r="3" className="brand-head" />
          </svg>
          <span className="brand-word">stiggy</span>
        </a>
        <div className="top-state">
          {ov.data?.read_only && <span className="pill">read-only</span>}
          <span className={`conn${live ? " on" : ""}`} title={live ? "Live: executions update as they end" : "Not connected: refreshing every few seconds"}>
            <span className="conn-dot" aria-hidden />
            {live ? "Live" : "Reconnecting"}
          </span>
          {ov.at && <span className="muted small">updated {ago(new Date(ov.at).toISOString(), now)}</span>}
        </div>
      </header>

      <main className="main">
        {ov.error && !ov.data && (
          <div className="empty">
            <h2>The dashboard cannot reach NATS</h2>
            <p className="error">{ov.error}</p>
            <p>Check that NATS is running and that <code>stiggy dashboard</code> was given the right <code>-server</code>.</p>
          </div>
        )}
        {route.page === "overview" && ov.data && <Overview data={ov.data} refresh={ov.refresh} />}
        {route.page === "namespace" && <NamespacePage ns={route.ns} flow={route.flow} status={route.status} overview={ov.data} />}
        {route.page === "execution" && <ExecutionPage ns={route.ns} id={route.id} readOnly={ov.data?.read_only ?? true} />}
      </main>
    </div>
  );
}
