// The whole execution, top to bottom: every agent's answer, every human
// reply, every routing decision, in the order they happened.

import { useState } from "react";

import type * as api from "./api";
import { Fields, Prose } from "./Bits";
import { clock, duration, money, plural, tokens } from "./format";

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
  const shown = story.filter(
    (e) =>
      (!focus || e.node === focus) &&
      (filter === "all" ||
      (filter === "humans" && (e.kind === "human" || e.asks)) ||
      (filter === "agents" && (e.kind === "step" || e.kind === "start" || e.kind === "end"))),
  );

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
            key={i}
            e={e}
            future={until !== undefined && Date.parse(e.end ?? e.time) > until}
            selected={!!e.node && e.node === selected}
            onSelect={onSelect}
          />
        ))}
      </ol>
    </div>
  );
}

function StoryEntry({ e, future, selected, onSelect }: { e: api.Entry; future: boolean; selected: boolean; onSelect: (n: string) => void }) {
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
          {e.output && e.output.length > 0 && <Fields fields={e.output} empty="" />}
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
      return <StepEntry e={e} cls={cls} node={node} />;
  }
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

function StepEntry({ e, cls, node }: { e: api.Entry; cls: string; node: React.ReactNode }) {
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
    </li>
  );
}
