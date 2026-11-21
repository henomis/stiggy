// What one attempt of an agent step did, from its record: what was sent to
// the model, each model call as a step of a list, the tool calls and the
// nested runs, as text to read.

import { createContext, useContext, useEffect, useId, useRef, useState, type ReactNode } from "react";

import * as api from "./api";
import { Prose } from "./Bits";
import { duration, money, plural, tokens } from "./format";
import { href } from "./hooks";
import {
  answerPlan,
  bytes,
  defaultOpen,
  ledgerParts,
  matchChildren,
  detailsLabel,
  lastAnswer,
  recId,
  stepPrompt,
  textBytes,
  type Attempt,
} from "./recordNav";

/** The records of an execution's attempts: which are open, which the page
 * asked to show, and those already fetched (kept across polls). */
export interface RecordNav {
  ns: string;
  id: string;
  isOpen: (rid: string) => boolean;
  /** Opens or closes a record where it is. */
  setOpen: (at: Attempt, open: boolean) => void;
  /** Opens a record, scrolls to it and moves the focus to it. */
  show: (at: Attempt) => void;
  /** True once for the record show asked for last, when rid is it. */
  take: (rid: string) => boolean;
  /** Changes each time show is called. */
  target?: { rid: string; n: number };
  cache: Map<string, api.AttemptRecord>;
}

export const RecordNavContext = createContext<RecordNav | undefined>(undefined);

/** Expand all: every fold of a record opened, every text shown whole. gen
 * counts the presses, so that a fold follows each one. */
const Expand = createContext({ all: false, gen: 0 });

/** Prose that shows all of itself on Expand all. */
function P(props: { text: string; lines?: number }) {
  return <Prose {...props} open={useContext(Expand).all} />;
}

/** A details element that follows Expand all, and the reader otherwise. */
function Fold({ summary, open: initial = false, className, children }: { summary: ReactNode; open?: boolean; className?: string; children: ReactNode }) {
  const ex = useContext(Expand);
  const [open, setOpen] = useState(ex.gen > 0 ? ex.all : initial);
  const gen = useRef(ex.gen);
  useEffect(() => {
    if (gen.current === ex.gen) return;
    gen.current = ex.gen;
    setOpen(ex.all);
  }, [ex.gen, ex.all]);
  return (
    <details className={className ?? "rec-fold"} open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>{summary}</summary>
      <div className="rec-fold-body">{children}</div>
    </details>
  );
}

export const CUT_TITLE = "Only the start was kept (record_max_bytes)";
export const Cut = ({ dot }: { dot?: boolean }) => (
  <span className="rec-cutmark" title={CUT_TITLE}>
    {dot ? " · cut" : " (cut)"}
  </span>
);

/** The record of an attempt, from the page's cache or fetched into it:
 * the story's Input and Details share it. */
export async function loadRecord(nav: RecordNav, at: Attempt): Promise<api.AttemptRecord> {
  const rid = recId(at);
  const kept = nav.cache.get(rid);
  if (kept) return kept;
  const r = await api.record(nav.ns, nav.id, at);
  nav.cache.set(rid, r);
  return r;
}

/** Where else the story entry shows the attempt's answer (see answerPlan). */
export type ShownAs = "fields" | "text" | undefined;

/** The Details link of one attempt, and its record once loaded. */
export function RecordToggle({
  at,
  outcome,
  usage,
  role,
  siblings,
  shownAs,
  promptAbove,
}: {
  at: Attempt;
  outcome?: string;
  usage?: api.Usage;
  role?: string;
  /** The recorded attempts of the same step, this one among them. */
  siblings: Attempt[];
  /** How the story entry shows the answer. */
  shownAs: ShownAs;
  /** The story entry shows the step prompt. */
  promptAbove: boolean;
}) {
  const nav = useContext(RecordNavContext)!;
  const rid = recId(at);
  const open = nav.isOpen(rid);
  const [rec, setRec] = useState<api.AttemptRecord | undefined>(() => nav.cache.get(rid));
  const [err, setErr] = useState<{ status?: number; msg: string }>();
  const [tries, setTries] = useState(0);
  const [ex, setEx] = useState({ all: false, gen: 0 });
  const panelId = useId();
  const root = useRef<HTMLDivElement>(null);
  const head = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    if (!open || rec) return;
    let live = true;
    setErr(undefined);
    loadRecord(nav, at)
      .then((r) => live && setRec(r))
      .catch((e: Error) => live && setErr({ status: e instanceof api.ApiError ? e.status : undefined, msg: e.message }));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, rec, rid, tries, nav.ns, nav.id]);

  // Asked to show this record (a link, a sibling, the side panel): bring it
  // into view and put the focus on it.
  useEffect(() => {
    if (!open || !nav.take(rid)) return;
    root.current?.closest("li")?.scrollIntoView({ block: "start" });
    head.current?.focus({ preventScroll: true });
  });

  const i = siblings.findIndex((s) => recId(s) === rid);
  const prev = i > 0 ? siblings[i - 1] : undefined;
  const next = i >= 0 && i < siblings.length - 1 ? siblings[i + 1] : undefined;
  const remote = rec?.record?.via === "remote";

  return (
    <div className="rec-toggle" ref={root}>
      <button type="button" className="link rec-details" aria-expanded={open} aria-controls={panelId} onClick={() => nav.setOpen(at, !open)}>
        {detailsLabel(outcome, usage, open, role)}
      </button>
      {open && (
        <div id={panelId} className="rec" role="region" aria-label={`Record of ${at.node}, attempt ${at.attempt}`}>
          <div className="rec-top">
            <h4 className="rec-title" ref={head} tabIndex={-1}>
              {siblings.length > 1 && i >= 0 ? `Attempt ${i + 1} of ${siblings.length}` : "Record"}
            </h4>
            {siblings.length > 1 && i >= 0 && (
              <span className="rec-nav">
                <button type="button" className="link" disabled={!prev} onClick={() => prev && nav.show(prev)}>
                  <span aria-hidden>◂ </span>previous
                </button>
                <button type="button" className="link" disabled={!next} onClick={() => next && nav.show(next)}>
                  next<span aria-hidden> ▸</span>
                </button>
              </span>
            )}
            {rec?.record && !remote && (
              <button type="button" className="link rec-expand" onClick={() => setEx({ all: !ex.all, gen: ex.gen + 1 })}>
                {ex.all ? "Collapse all" : "Expand all"}
              </button>
            )}
            <CopyLink at={at} />
          </div>
          {rec ? (
            <Expand.Provider value={ex}>
              <AttemptView a={rec} shownAs={shownAs} promptAbove={promptAbove} />
            </Expand.Provider>
          ) : err?.status === 404 ? (
            <p className="muted rec-state">No record was kept for this attempt.</p>
          ) : err ? (
            <p className="rec-state">
              <span className="entry-error">Could not load the record: {err.msg}</span>{" "}
              <button
                type="button"
                className="link"
                onClick={() => {
                  setErr(undefined);
                  setTries(tries + 1);
                }}
              >
                Try again
              </button>
            </p>
          ) : (
            <p className="rec-loading rec-state" aria-busy="true">
              Loading the record…
            </p>
          )}
        </div>
      )}
    </div>
  );
}

/** Copies a link that opens this record; where the clipboard is out of
 * reach, shows the link to copy by hand. */
function CopyLink({ at }: { at: Attempt }) {
  const nav = useContext(RecordNavContext)!;
  const [said, setSaid] = useState<"copied" | "manual">();
  const url = location.origin + location.pathname + location.search + href.execution(nav.ns, nav.id, at);

  useEffect(() => {
    if (said !== "copied") return;
    const t = setTimeout(() => setSaid(undefined), 2500);
    return () => clearTimeout(t);
  }, [said]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setSaid("copied");
    } catch {
      setSaid(copyByHand(url) ? "copied" : "manual");
    }
  };

  return (
    <span className="rec-link">
      <button type="button" className="icon-btn" aria-label="Copy link to this record" title="Copy link to this record" onClick={copy}>
        <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden>
          <path d="M6.5 9.5l3-3M7 4.5l1.2-1.2a2.6 2.6 0 013.7 3.7L10.7 8.2M9 11.5l-1.2 1.2a2.6 2.6 0 01-3.7-3.7L5.3 7.8" />
        </svg>
      </button>
      <span role="status" className="rec-copied">
        {said === "copied" && "Link copied"}
        {said === "manual" && (
          <input className="rec-url" readOnly value={url} aria-label="Link to this record" onFocus={(e) => e.currentTarget.select()} autoFocus />
        )}
      </span>
    </span>
  );
}

/** The old way to copy, for pages served without a secure context. */
function copyByHand(text: string): boolean {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  ta.remove();
  return ok;
}

function AttemptView({ a, shownAs, promptAbove }: { a: api.AttemptRecord; shownAs: ShownAs; promptAbove: boolean }) {
  if (a.too_large) {
    return (
      <div className="rec-run">
        <p className="rec-cut">Record too large ({bytes(a.too_large.size)}): only its start was kept.</p>
        <Fold summary="Show the start of the raw record">
          <div className="rec-args">
            <P text={a.too_large.head} lines={6} />
          </div>
        </Fold>
      </div>
    );
  }
  const r = a.record;
  if (!r) return <p className="muted rec-state">No record was kept for this attempt.</p>;
  return (
    <>
      {r.truncated && <p className="rec-cut">This record was cut to fit its size limit: items marked “cut” keep only their start.</p>}
      <RunView run={r} top={r} traceURL={a.trace_url} shownAs={shownAs} promptAbove={promptAbove} path={[r.agent]} />
    </>
  );
}

const span = (a?: string, b?: string) => (a && b ? Date.parse(b) - Date.parse(a) : 0);

const STOP: Record<string, string> = {
  max_iterations: "reached its iteration limit",
  handoff: "handed off",
  stopped: "was stopped by a tool",
  cancelled: "was cancelled",
  guardrail: "was blocked by a guardrail",
  error: "failed",
};

const inOut = (u: api.RecordUsage | undefined) =>
  u && u.input_tokens + u.output_tokens ? `${tokens(u.input_tokens)} in / ${tokens(u.output_tokens)} out` : "";

/** A run's facts, in one line. */
function runFacts(run: api.RecordRun): string[] {
  const f: string[] = [];
  if (run.remote) f.push(`remote agent ${run.remote}`);
  if (run.model) f.push(run.model);
  const took = span(run.start, run.end);
  if (took) f.push(duration(took));
  const io = inOut(run.usage);
  if (io) f.push(io);
  if (run.usage?.cost_usd) f.push(money(run.usage.cost_usd));
  if (run.ttft_ms) f.push(`first token in ${duration(run.ttft_ms)}`);
  return f;
}

function RunView({
  run,
  top,
  traceURL,
  shownAs,
  promptAbove,
  path,
}: {
  run: api.RecordRun;
  top?: api.StepRecord;
  traceURL?: string;
  /** Where else the run's answer shows (see answerPlan). */
  shownAs?: "fields" | "text" | "tool";
  /** The story entry shows the run's step prompt: it is not repeated. */
  promptAbove?: boolean;
  /** The agents from the step's own down to this run. */
  path: string[];
}) {
  const calls = run.calls ?? [];
  const remote = run.via === "remote";
  const nested = path.length > 1;
  const { byCall, rest } = matchChildren(calls, run.children ?? []);
  const failedCall = [...calls].reverse().find((c) => c.error);
  const error = run.error ?? failedCall?.error;
  const plan = answerPlan(shownAs, run);
  const sent = calls[0]?.messages ?? [];
  const above = promptAbove ? stepPrompt(run)?.index : undefined;
  const aboveAnswer = shownAs === "text" ? lastAnswer(run)?.text : undefined;
  const after: string[] = [];
  if (run.stop_reason && run.stop_reason !== "final") after.push(STOP[run.stop_reason] ?? run.stop_reason);
  if (run.iterations && run.iterations > 1) after.push(`${plural(run.iterations, "iteration")}${run.max_iterations ? ` of ${run.max_iterations}` : ""}`);
  const facts = runFacts(run);
  const fieldCount = top?.output_schema ? Object.keys((top.output_schema.properties ?? {}) as object).length : 0;

  return (
    <div className={`rec-run${nested ? " rec-nested" : ""}`}>
      {nested && (
        <p className="rec-path">
          <span className="rec-path-short">
            {run.agent}
            <span className="muted">{run.via === "delegate" ? `, asked through ${run.tool}` : ", run inside this step"}</span>
          </span>
          <span className="rec-path-full">{path.join(" › ")}</span>
        </p>
      )}
      {(facts.length > 0 || after.length > 0 || traceURL) && (
        <p className="rec-facts">
          {[...facts, ...after].join(" · ")}
          {traceURL && (
            <>
              {facts.length + after.length > 0 ? " · " : ""}
              <a href={traceURL} target="_blank" rel="noreferrer">
                trace
              </a>
            </>
          )}
        </p>
      )}
      {error && (
        <div className="entry-error rec-error">
          <P text={error} lines={4} />
        </div>
      )}
      {!remote && (run.system || run.response_format || fieldCount > 0 || run.tools?.length || run.memory) && (
        <div className="rec-folds">
          {run.system && (
            <Fold
              summary={
                <>
                  System prompt · {bytes(textBytes(run.system))}
                  {run.system_truncated && <Cut dot />}
                </>
              }
            >
              <P text={run.system} lines={12} />
            </Fold>
          )}
          {(run.response_format || fieldCount > 0) && (
            <Fold summary={`Answer format · ${fieldCount ? plural(fieldCount, "field") : run.response_format}`}>
              {top?.output_schema ? <Schema schema={top.output_schema} /> : <p className="muted">Structured output.</p>}
            </Fold>
          )}
          {run.tools && run.tools.length > 0 && (
            <Fold summary={`Tools offered (${run.tools.length})`}>
              <p className="rec-chips">
                {run.tools.map((t) => (
                  <span key={t} className="route-chip">
                    {t}
                  </span>
                ))}
              </p>
            </Fold>
          )}
          {run.memory && <MemoryView m={run.memory} />}
        </div>
      )}
      {sent.length > 0 && (
        <ul className="rec-msgs rec-sent">
          {sent.map((m, i) =>
            i === above ? (
              <li key={i} className="rec-msg r-user">
                <span className="rec-role">{remote ? "Asked" : "Sent"}</span>
                <span className="muted">Prompt shown above</span>
              </li>
            ) : (
              <MessageView key={i} m={m} user={remote ? "Asked" : "Sent"} />
            ),
          )}
        </ul>
      )}
      {remote && (
        <p className="rec-note">
          {shownAs === "text" ? "The answer is shown above. " : ""}Its own prompt and tool calls stay where it is served.
        </p>
      )}
      {!remote && calls.length === 1 && (calls[0].tool_calls ?? []).length > 0 && (
        <ul className="rec-tool-calls">
          {calls[0].tool_calls!.map((t, i) => (
            <ToolCallView key={i} t={t} child={byCall.get(t)} path={path} />
          ))}
        </ul>
      )}
      {!remote && calls.length >= 2 && (
        <Ledger run={run} byCall={byCall} path={path} answer={plan.kind === "none" ? aboveAnswer : plan.text} above={!!aboveAnswer} />
      )}
      {rest.map((ch, i) => (
        <RunView key={i} run={ch} path={[...path, ch.agent]} />
      ))}
      {plan.kind === "raw" && (
        <Fold
          summary={
            <>
              Raw answer (shown above as fields)
              {plan.cut && <Cut />}
            </>
          }
        >
          <P text={plan.text} lines={10} />
        </Fold>
      )}
      {plan.kind === "open" && (
        <div className="rec-block">
          <h5>
            {nested ? "Its answer" : "Last answer from the model"}
            {plan.cut && <Cut />}
          </h5>
          <P text={plan.text} lines={10} />
        </div>
      )}
    </div>
  );
}

/** Consecutive calls of one tool with one outcome, as one chip. */
function chipGroups(ts: api.RecordToolCall[]): { tool: string; failed: boolean; n: number }[] {
  const out: { tool: string; failed: boolean; n: number }[] = [];
  for (const t of ts) {
    const last = out[out.length - 1];
    if (last && last.tool === t.tool && last.failed === !!t.error) last.n++;
    else out.push({ tool: t.tool, failed: !!t.error, n: 1 });
  }
  return out;
}

function ToolChips({ ts }: { ts: api.RecordToolCall[] }) {
  return (
    <>
      {chipGroups(ts).map((g, i) => (
        <span key={i} className={`route-chip tool-chip${g.failed ? " failed" : ""}`}>
          {g.failed && <span aria-hidden>✕ </span>}
          {g.tool}
          {g.n > 1 && ` ×${g.n}`}
          {g.failed && <span className="sr-only"> failed</span>}
        </span>
      ))}
    </>
  );
}

/** The model calls of a run, one line each; a line opens on what the call
 * was given, what it answered and the tools it called. */
function Ledger({
  run,
  byCall,
  path,
  answer,
  above,
}: {
  run: api.RecordRun;
  byCall: Map<api.RecordToolCall, api.RecordRun>;
  path: string[];
  answer?: string;
  /** The answer is shown in the story entry, not below. */
  above?: boolean;
}) {
  const calls = run.calls ?? [];
  const parts = ledgerParts(calls);
  return (
    <div className="rec-ledger">
      {calls.length >= 3 && <CallTimeline calls={calls} start={run.start} end={run.end} />}
      <ol className="rec-steps">
        {calls.map((c, i) => {
          const last = i === calls.length - 1;
          const ts = c.tool_calls ?? [];
          const toolFailed = ts.some((t) => t.error);
          const facts = [c.compaction ? "summary of the conversation" : c.model, duration(span(c.start, c.end)), inOut(c.usage)].filter(
            (x) => x && x !== "0ms",
          );
          const p = parts[i];
          const empty = !p.msgs.length && !p.reasoning && !c.error && !c.response && !ts.length;
          return (
            <li key={i}>
              <Fold
                className={`rec-step${c.error || toolFailed ? " failed" : ""}`}
                open={defaultOpen(c, last)}
                summary={
                  <>
                    <span className="rec-step-head">
                      <span className="rec-step-n">{i + 1}</span>
                      <span className="rec-step-facts">
                        {facts.join(" · ")}
                        {c.error && <span className="rec-failed"> · failed</span>}
                      </span>
                    </span>
                    {ts.length > 0 && (
                      <span className="rec-step-tools">
                        <span aria-hidden>→ </span>
                        <ToolChips ts={ts} />
                      </span>
                    )}
                  </>
                }
              >
                {p.msgs.length > 0 && (
                  <ul className="rec-msgs">
                    {p.msgs.map((m, j) => (
                      <MessageView key={j} m={m} user="Sent" />
                    ))}
                  </ul>
                )}
                {p.reasoning && (
                  <div className="rec-reasoning">
                    <span className="rec-role">Reasoning</span>
                    <P text={p.reasoning} lines={3} />
                  </div>
                )}
                {c.error && (
                  <div className="entry-error">
                    <P text={c.error} lines={4} />
                  </div>
                )}
                {c.response &&
                  (last && answer === c.response ? (
                    <p className="rec-note">Its answer is shown {above ? "above" : "below"}.</p>
                  ) : (
                    <div className="rec-response">
                      <span className="rec-role">
                        Answer
                        {c.response_truncated && <Cut />}
                      </span>
                      <P text={c.response} lines={8} />
                    </div>
                  ))}
                {ts.length > 0 && (
                  <ul className="rec-tool-calls">
                    {ts.map((t, j) => (
                      <ToolCallView key={j} t={t} child={byCall.get(t)} path={path} />
                    ))}
                  </ul>
                )}
                {empty && i > 0 && <p className="muted">Nothing else was kept for this call.</p>}
              </Fold>
            </li>
          );
        })}
      </ol>
    </div>
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
    <Fold summary={`Memory · ${m.retrieved} retrieved, ${m.saved} saved`}>
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

const ROLE: Record<string, string> = { assistant: "Model", tool: "Tool result", system: "System" };

function MessageView({ m, label, user = "Sent" }: { m: api.RecordMessage; label?: string; user?: string }) {
  const who = [label, m.role === "user" ? user : ROLE[m.role] ?? m.role, m.name].filter(Boolean).join(" · ");
  return (
    <li className={`rec-msg r-${m.role}${m.tool_error ? " failed" : ""}`}>
      <span className="rec-role">
        {who}
        {m.tool_error ? " (failed)" : ""}
        {m.truncated && <Cut />}
      </span>
      {m.reasoning && (
        <div className="rec-reasoning">
          <P text={m.reasoning} lines={3} />
        </div>
      )}
      {m.text ? <P text={m.text} lines={8} /> : !m.tool_calls?.length && <span className="muted">(empty)</span>}
      {m.tool_calls && m.tool_calls.length > 0 && <span className="muted">calls {m.tool_calls.join(", ")}</span>}
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

/** One tool call: its arguments, the run it started (a delegate), then
 * its result or error. */
function ToolCallView({ t, child, path }: { t: api.RecordToolCall; child?: api.RecordRun; path: string[] }) {
  const took = span(t.start, t.end);
  return (
    <li className={`rec-tool${t.error ? " failed" : ""}`}>
      <div className="rec-call-head">
        <span className={`route-chip tool-chip${t.error ? " failed" : ""}`}>
          {t.error && <span aria-hidden>✕ </span>}
          {t.tool}
        </span>
        <span className="muted">
          {t.error && <span className="rec-failed">failed</span>}
          {t.error && took ? " · " : ""}
          {took ? duration(took) : ""}
          {t.truncated && <Cut dot />}
        </span>
      </div>
      {t.args && (
        <div className="rec-args">
          <P text={argsText(t.args)} lines={4} />
        </div>
      )}
      {child && <RunView run={child} shownAs="tool" path={[...path, child.agent]} />}
      {t.error && (
        <div className="entry-error">
          <P text={t.error} lines={3} />
        </div>
      )}
      {t.result && (
        <div className="rec-result">
          <P text={t.result} lines={5} />
        </div>
      )}
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
