// What one attempt of an agent step did, from its record: the prompt sent
// to the model, each model call, the tool calls and the nested runs, as
// text to read.

import { useEffect, useState } from "react";

import * as api from "./api";
import { Prose } from "./Bits";
import { duration, money, plural, tokens } from "./format";

type Attempt = { node: string; key: string; generation: number; attempt: number };

/** The toggle of one attempt's record, and the record once loaded. */
export function RecordToggle({ ns, id, at }: { ns: string; id: string; at: Attempt }) {
  const [open, setOpen] = useState(false);
  const [rec, setRec] = useState<api.AttemptRecord>();
  const [err, setErr] = useState<string>();

  useEffect(() => {
    if (!open || rec) return;
    let live = true;
    api
      .record(ns, id, at)
      .then((r) => live && setRec(r))
      .catch((e: Error) => live && setErr(e.message));
    return () => {
      live = false;
    };
  }, [open, rec, ns, id, at.node, at.key, at.generation, at.attempt]);

  return (
    <div className="rec-toggle">
      <button type="button" className="link" aria-expanded={open} onClick={() => setOpen(!open)}>
        {open ? "Hide the prompt" : "Show the prompt"}
      </button>
      {open && (
        <div className="rec">
          {err ? <p className="entry-error">Could not load the record: {err}</p> : !rec ? <p className="muted">Loading…</p> : <AttemptView a={rec} />}
        </div>
      )}
    </div>
  );
}

function AttemptView({ a }: { a: api.AttemptRecord }) {
  if (a.too_large) {
    return (
      <>
        <p className="rec-cut">Record too large ({tokens(a.too_large.size)} bytes): only its start was kept.</p>
        <Prose text={a.too_large.head} lines={6} />
      </>
    );
  }
  const r = a.record;
  if (!r) return <p className="muted">No record.</p>;
  return (
    <>
      {r.truncated && <p className="rec-cut">This record was cut to fit its size limit: items marked “cut” keep only their start.</p>}
      <RunView run={r} top={r} traceURL={a.trace_url} />
    </>
  );
}

function usageFacts(u: api.RecordUsage | undefined, out: string[]) {
  if (!u) return;
  const n = u.input_tokens + u.output_tokens;
  if (n) out.push(`${tokens(u.input_tokens)} in, ${tokens(u.output_tokens)} out`);
  if (u.cache_read_tokens) out.push(`${tokens(u.cache_read_tokens)} cached`);
  if (u.reasoning_tokens) out.push(`${tokens(u.reasoning_tokens)} reasoning`);
  if (u.cost_usd) out.push(money(u.cost_usd));
}

const span = (a?: string, b?: string) => (a && b ? Date.parse(b) - Date.parse(a) : 0);

const STOP: Record<string, string> = {
  final: "answered",
  max_iterations: "reached its iteration limit",
  handoff: "handed off",
  stopped: "was stopped by a tool",
  cancelled: "was cancelled",
  guardrail: "was blocked by a guardrail",
  error: "failed",
};

function RunView({ run, top, traceURL }: { run: api.RecordRun; top?: api.StepRecord; traceURL?: string }) {
  const facts: string[] = [];
  if (run.model) facts.push(run.model);
  const took = span(run.start, run.end);
  if (took) facts.push(duration(took));
  if (run.stop_reason) facts.push(STOP[run.stop_reason] ?? run.stop_reason);
  if (run.iterations) facts.push(`${plural(run.iterations, "iteration")}${run.max_iterations ? ` of ${run.max_iterations}` : ""}`);
  if (run.ttft_ms) facts.push(`first token in ${duration(run.ttft_ms)}`);
  usageFacts(run.usage, facts);

  const calls = run.calls ?? [];

  return (
    <div className="rec-run">
      <p className="rec-facts">
        {facts.join(" · ")}
        {traceURL && (
          <>
            {" · "}
            <a href={traceURL} target="_blank" rel="noreferrer">
              trace
            </a>
          </>
        )}
      </p>
      {run.error && (
        <div className="entry-error">
          <Prose text={run.error} lines={4} />
        </div>
      )}
      {run.system && (
        <Fold title={`System prompt${run.system_truncated ? " (cut)" : ""}`}>
          <Prose text={run.system} lines={12} />
        </Fold>
      )}
      {(run.response_format || top?.output_schema) && (
        <Fold title={`Answer format${run.response_format ? `: ${run.response_format}` : ""}`}>
          {top?.output_schema ? <Schema schema={top.output_schema} /> : <p className="muted">Structured output.</p>}
        </Fold>
      )}
      {run.tools && run.tools.length > 0 && (
        <p className="rec-tools">
          <span className="muted">Tools offered:</span>{" "}
          {run.tools.map((t) => (
            <span key={t} className="route-chip">
              {t}
            </span>
          ))}
        </p>
      )}
      {run.memory && <MemoryView m={run.memory} />}
      {calls.length > 1 && <CallTimeline calls={calls} start={run.start} end={run.end} />}
      <ol className="rec-calls">
        {calls.map((c, i) => (
          <CallView key={i} c={c} n={i + 1} />
        ))}
      </ol>
      {run.output && (
        <div className="rec-block">
          <h4>Final answer{run.output_truncated ? " (cut)" : ""}</h4>
          <Prose text={run.output} lines={10} />
        </div>
      )}
      {run.children && run.children.length > 0 && (
        <div className="rec-children">
          {run.children.map((ch, i) => (
            <Fold key={i} title={`${ch.agent}${ch.via === "delegate" ? `, asked through ${ch.tool}` : ", run inside this step"}`}>
              <RunView run={ch} />
            </Fold>
          ))}
        </div>
      )}
    </div>
  );
}

function Fold({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <details className="rec-fold">
      <summary>{title}</summary>
      <div className="rec-fold-body">{children}</div>
    </details>
  );
}

const typeName = (t: string | string[] | undefined) => (Array.isArray(t) ? t.join(" or ") : t ?? "any");

/** A JSON schema's top-level fields, as a list. */
function Schema({ schema }: { schema: Record<string, unknown> }) {
  const props = (schema.properties ?? {}) as Record<string, { type?: string | string[]; description?: string }>;
  const required = new Set((schema.required as string[] | undefined) ?? []);
  const keys = Object.keys(props);
  if (!keys.length) return <p className="muted">An answer of type {String(schema.type ?? "any")}.</p>;
  return (
    <ul className="rec-schema">
      {keys.map((k) => (
        <li key={k}>
          <code>{k}</code> <span className="muted">{typeName(props[k].type)}</span>
          {required.has(k) ? "" : <span className="muted">, optional</span>}
          {props[k].description && <span> — {props[k].description}</span>}
        </li>
      ))}
    </ul>
  );
}

function MemoryView({ m }: { m: api.RecordMemory }) {
  const items = [...(m.retrieved_items ?? []).map((x) => ["retrieved", x] as const), ...(m.saved_items ?? []).map((x) => ["saved", x] as const)];
  return (
    <Fold title={`Memory: ${plural(m.retrieved, "message")} retrieved, ${m.saved} saved`}>
      {items.length === 0 ? (
        <p className="muted">No messages kept in the record.</p>
      ) : (
        <ul className="rec-msgs">
          {items.map(([op, x], i) => (
            <MessageView key={i} m={x} label={op} />
          ))}
        </ul>
      )}
    </Fold>
  );
}

const ROLE: Record<string, string> = { user: "Prompt", assistant: "Model", tool: "Tool result", system: "System" };

function MessageView({ m, label }: { m: api.RecordMessage; label?: string }) {
  const who = [label, ROLE[m.role] ?? m.role, m.name].filter(Boolean).join(" · ");
  return (
    <li className={`rec-msg r-${m.role}${m.tool_error ? " failed" : ""}`}>
      <span className="rec-role">
        {who}
        {m.tool_error ? " (failed)" : ""}
        {m.truncated ? " (cut)" : ""}
      </span>
      {m.reasoning && (
        <div className="rec-reasoning">
          <Prose text={m.reasoning} lines={3} />
        </div>
      )}
      {m.text ? <Prose text={m.text} lines={8} /> : !m.tool_calls?.length && <span className="muted">(empty)</span>}
      {m.tool_calls && m.tool_calls.length > 0 && <span className="muted">calls {m.tool_calls.join(", ")}</span>}
    </li>
  );
}

function CallView({ c, n }: { c: api.RecordCall; n: number }) {
  const facts: string[] = [];
  if (c.model) facts.push(c.model);
  const took = span(c.start, c.end);
  if (took) facts.push(duration(took));
  if (c.ttft_ms) facts.push(`first token in ${duration(c.ttft_ms)}`);
  usageFacts(c.usage, facts);
  if (c.finish_reason && c.finish_reason !== "stop") facts.push(`stopped: ${c.finish_reason}`);
  const msgs = c.messages ?? [];

  return (
    <li className={`rec-call${c.error ? " failed" : ""}`}>
      <div className="rec-call-head">
        <strong>{c.compaction ? "Summary of the conversation" : `Model call ${n}`}</strong>
        <span className="muted">{facts.join(" · ")}</span>
      </div>
      {msgs.length > 0 && (
        <>
          {c.message_offset > 0 && <p className="rec-note">New since the previous call (from message {c.message_offset + 1}):</p>}
          <ul className="rec-msgs">
            {msgs.map((m, i) => (
              <MessageView key={i} m={m} />
            ))}
          </ul>
        </>
      )}
      {c.error && (
        <div className="entry-error">
          <Prose text={c.error} lines={4} />
        </div>
      )}
      {c.response && (
        <div className="rec-response">
          <span className="rec-role">Answer{c.response_truncated ? " (cut)" : ""}</span>
          <Prose text={c.response} lines={8} />
        </div>
      )}
      {c.tool_calls && c.tool_calls.length > 0 && (
        <ul className="rec-tool-calls">
          {c.tool_calls.map((t, i) => (
            <ToolCallView key={i} t={t} />
          ))}
        </ul>
      )}
    </li>
  );
}

/** A tool's JSON arguments as key: value lines; anything else as is. */
export function argsText(args: string): string {
  try {
    const v: unknown = JSON.parse(args);
    if (v && typeof v === "object" && !Array.isArray(v)) {
      const lines = Object.entries(v as Record<string, unknown>).map(([k, x]) => `${k}: ${typeof x === "string" ? x : JSON.stringify(x)}`);
      return lines.length ? lines.join("\n") : "(no arguments)";
    }
  } catch {
    // not JSON: shown as is
  }
  return args;
}

function ToolCallView({ t }: { t: api.RecordToolCall }) {
  const took = span(t.start, t.end);
  return (
    <li className={`rec-tool${t.error ? " failed" : ""}`}>
      <div className="rec-call-head">
        <span className="route-chip">{t.tool}</span>
        <span className="muted">
          {[t.error ? "failed" : "", took ? duration(took) : "", t.truncated ? "cut" : ""].filter(Boolean).join(" · ")}
        </span>
      </div>
      {t.args && (
        <div className="rec-args">
          <Prose text={argsText(t.args)} lines={4} />
        </div>
      )}
      {t.error && (
        <div className="entry-error">
          <Prose text={t.error} lines={3} />
        </div>
      )}
      {t.result && <Prose text={t.result} lines={5} />}
    </li>
  );
}

/** The calls of a run over time, one bar each. */
function CallTimeline({ calls, start, end }: { calls: api.RecordCall[]; start?: string; end?: string }) {
  const times = calls.flatMap((c) => [c.start, c.end, ...(c.tool_calls ?? []).flatMap((t) => [t.start, t.end])]).filter(Boolean).map((t) => Date.parse(t!));
  const t0 = start ? Date.parse(start) : Math.min(...times);
  const t1 = Math.max(end ? Date.parse(end) : 0, ...times, t0 + 1);
  const pct = (t: number) => ((t - t0) / (t1 - t0)) * 100;
  const bar = (a: string | undefined, b: string | undefined, cls: string, title: string, key: string) =>
    a && b ? (
      <span
        key={key}
        className={`tl-bar ${cls}`}
        style={{ left: `${pct(Date.parse(a))}%`, width: `max(3px, ${pct(Date.parse(b)) - pct(Date.parse(a))}%)` }}
        title={title}
      />
    ) : null;

  return (
    <div className="timeline rec-timeline" role="table" aria-label="Model and tool calls over time">
      {calls.map((c, i) => (
        <div key={i} className="tl-row" role="row">
          <span className="tl-lane-name" role="rowheader">
            {c.compaction ? "summary" : `call ${i + 1}`}
          </span>
          <span className="tl-track" role="cell">
            {bar(c.start, c.end, c.error ? "o-failed" : "o-done", `call ${i + 1}: ${duration(span(c.start, c.end))}`, "c")}
            {(c.tool_calls ?? []).map((t, j) =>
              bar(t.start, t.end, t.error ? "o-failed" : "o-human", `${t.tool}: ${duration(span(t.start, t.end))}`, "t" + j),
            )}
          </span>
        </div>
      ))}
      <div className="tl-legend" aria-hidden>
        <span className="o-done">model call</span>
        <span className="o-human">tool call</span>
        <span className="o-failed">failed</span>
      </div>
    </div>
  );
}
