// The whole execution, top to bottom: every agent's answer, every human
// reply, every routing decision, in the order they happened.

import { useContext, useEffect, useMemo, useState } from "react";

import type * as api from "./api";
import { Fields, Prose } from "./Bits";
import { RecordNavContext, RecordToggle } from "./Record";
import { clock, duration, money, plural, tokens } from "./format";
import { entryAttempt, recId, type Attempt } from "./recordNav";

interface Props {
  story: api.Entry[];
  /** While replaying, entries after this time have not happened yet. */
  until?: number;
  selected?: string;
  /** When set, only this step's entries (and the replies to it) are shown. */
  focus?: string;
  onSelect: (node: string) => void;
  onClearFocus: () => void;
}

type Filter = "all" | "agents" | "humans";

export function Story({ story, until, selected, focus, onSelect, onClearFocus }: Props) {
  const [filter, setFilter] = useState<Filter>("all");
  const nav = useContext(RecordNavContext);
  const shown = story.filter(
    (e) =>
      (!focus || e.node === focus) &&
      (filter === "all" ||
      (filter === "humans" && (e.kind === "human" || e.asks)) ||
      (filter === "agents" && (e.kind === "step" || e.kind === "start" || e.kind === "end"))),
  );

  // The recorded attempts of each step, in story order, for moving between
  // them whatever the filter shows.
  const attempts = useMemo(() => {
    const m = new Map<string, Attempt[]>();
    for (const e of story) {
      const a = entryAttempt(e);
      if (!a) continue;
      const k = a.node + "\u0000" + a.key;
      m.set(k, [...(m.get(k) ?? []), a]);
    }
    return m;
  }, [story]);

  // A record asked to be shown that the filter hides: show everything.
  const target = nav?.target;
  useEffect(() => {
    if (!target || filter === "all") return;
    const hidden = !shown.some((e) => {
      const a = entryAttempt(e);
      return a && recId(a) === target.rid;
    });
    if (hidden) setFilter("all");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target?.n]);

  return (
    <div className="story">
      <div className="filters story-filters" role="group" aria-label="Show">
        {(["all", "agents", "humans"] as const).map((f) => (
          <button key={f} type="button" className={`chip${filter === f ? " on" : ""}`} onClick={() => setFilter(f)}>
            {f === "all" ? "Everything" : f === "agents" ? "Steps only" : "Human exchanges"}
          </button>
        ))}
        {focus && (
          <span className="story-focus">
            Only <strong>{focus}</strong>
            <button type="button" className="link" onClick={onClearFocus}>
              Show every step
            </button>
          </span>
        )}
      </div>
      {focus && shown.length === 0 && <p className="muted">{focus} has not run yet.</p>}
      <ol className="story-list">
        {shown.map((e, i) => (
          <StoryEntry
            key={entryKey(e, i)}
            e={e}
            attempts={attempts}
            future={until !== undefined && Date.parse(e.end ?? e.time) > until}
            selected={!!e.node && e.node === selected}
            onSelect={onSelect}
          />
        ))}
      </ol>
    </div>
  );
}

/**
 * A key that follows an entry when the list is filtered, so the state of
 * an entry (an open record) stays with it. Entries of one attempt differ by
 * kind and time; the index only breaks ties among events of one instant.
 */
export function entryKey(e: api.Entry, i: number): string {
  if (e.kind === "step") return ["step", e.key ?? e.node, e.generation, e.attempt, e.outcome, e.end ?? e.time].join("|");
  return [e.kind, e.node, e.time, e.text, i].join("|");
}

function StoryEntry({
  e,
  attempts,
  future,
  selected,
  onSelect,
}: {
  e: api.Entry;
  attempts: Map<string, Attempt[]>;
  future: boolean;
  selected: boolean;
  onSelect: (n: string) => void;
}) {
  const cls = `entry k-${e.kind} o-${e.outcome ?? "none"}${e.asks ? " asks" : ""}${future ? " future" : ""}${selected ? " selected" : ""}`;
  const node = e.node ? (
    <button type="button" className="link node-link" onClick={() => onSelect(e.node!)}>
      {e.node}
    </button>
  ) : null;

  switch (e.kind) {
    case "start":
      return (
        <li className={cls}>
          <div className="entry-head">
            <span className="entry-who">Started</span>
            <span className="entry-when">{clock(e.time)}</span>
          </div>
          {e.text && <p className="entry-line">{e.text}</p>}
          {!e.text && <StartInput fields={e.output ?? []} />}
        </li>
      );
    case "end":
      return (
        <li className={cls}>
          <div className="entry-head">
            <span className="entry-who">
              {e.outcome === "done" ? "Completed" : e.outcome === "failed" ? "Failed" : "Cancelled"}
            </span>
            {node && <span className="muted">at {node}</span>}
            <span className="entry-when">{clock(e.time)}</span>
          </div>
          {(e.error || e.text) && <Prose text={[e.text, e.error].filter(Boolean).join(": ")} lines={4} />}
          {e.output && e.output.length > 0 && <Fields fields={e.output} empty="" />}
        </li>
      );
    case "human":
      return (
        <li className={cls}>
          <div className="bubble">
            <div className="entry-head">
              <span className="entry-who">{e.who || "A human"}</span>
              <span className="muted">{e.approved ? "approved" : "replied"} at {node}</span>
              <span className="entry-when">{clock(e.time)}</span>
            </div>
            {!e.approved && <Prose text={e.text || "(empty reply)"} lines={5} />}
          </div>
        </li>
      );
    case "event":
      return (
        <li className={cls}>
          <p className="entry-line">
            {node} {e.text} <span className="entry-when">{clock(e.time)}</span>
          </p>
        </li>
      );
    default:
      return <StepEntry e={e} cls={cls} node={node} attempts={attempts} />;
  }
}

/** The keys of an input that hold the prompt it was started with. */
const PROMPT_KEYS = ["prompt", "text", "message", "question"];

/** What the execution was started with, as a box: its prompt as text, the
 * other fields below it. */
function StartInput({ fields }: { fields: api.Field[] }) {
  const prompt = fields.find((f) => PROMPT_KEYS.includes(f.key) && f.kind === "text");
  const rest = fields.filter((f) => f !== prompt);
  return (
    <div className="entry-answer entry-input">
      <span className="rec-role">Started with</span>
      {fields.length === 0 && <p className="muted">No input: the flow was started with nothing to work on, by its schedule or by hand.</p>}
      {prompt && <Prose text={prompt.text} lines={8} />}
      {rest.length > 0 && <Fields fields={rest} empty="" />}
    </div>
  );
}

const OUTCOME: Record<string, string> = {
  done: "answered",
  cached: "answered from cache",
  retry: "failed, will retry",
  failed: "failed",
  waiting: "put it to a human",
  cancelled: "was cancelled",
  running: "is working",
};

function StepEntry({ e, cls, node, attempts }: { e: api.Entry; cls: string; node: React.ReactNode; attempts: Map<string, Attempt[]> }) {
  const at = entryAttempt(e);
  const took = e.end ? Date.parse(e.end) - Date.parse(e.time) : 0;
  const u = e.usage;
  const facts: string[] = [];
  if (e.visit && e.visit > 1) facts.push(`visit ${e.visit}`);
  if (e.attempt && e.attempt > 1) facts.push(`attempt ${e.attempt}`);
  if (took) facts.push(duration(took));
  if (u && (u.tokens_in || u.tokens_out)) facts.push(`${tokens(u.tokens_in + u.tokens_out)} tokens`);
  if (u?.cost_usd) facts.push(money(u.cost_usd));
  if (u?.tool_calls) facts.push(plural(u.tool_calls, "tool call") + (u.tool_errors ? `, ${u.tool_errors} failed` : ""));

  const verb = e.outcome === "waiting" ? (e.asks === "review" ? "asked for a review" : "asked a question") : OUTCOME[e.outcome ?? ""] ?? e.outcome;

  return (
    <li className={cls}>
      <div className="entry-head">
        <span className="entry-who">{e.who ?? e.node}</span>
        <span className="muted">
          {verb} at {node}
        </span>
        <span className="entry-when">{clock(e.end ?? e.time)}</span>
      </div>
      {facts.length > 0 && <p className="entry-facts">{facts.join(", ")}</p>}
      {e.outcome === "waiting" && <div className="entry-asks"><Prose text={e.text || "No text."} lines={8} /></div>}
      {e.kept && <p className="entry-line">Kept the approved answer.</p>}
      {e.output && e.output.length > 0 && (
        <div className="entry-answer">
          <Fields fields={e.output} empty="" />
        </div>
      )}
      {e.error && (
        <div className="entry-error">
          <Prose text={e.error} lines={4} />
        </div>
      )}
      {e.next && e.outcome !== "waiting" && (
        <p className="entry-route">
          sent the work to <span className="route-chip">{e.next}</span>
        </p>
      )}
      {at && (
        <RecordToggle
          key={recId(at)}
          at={at}
          outcome={e.outcome}
          siblings={attempts.get(at.node + "\u0000" + at.key) ?? [at]}
          hasOutput={!!e.output?.length}
        />
      )}
    </li>
  );
}
