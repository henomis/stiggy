// The whole execution, top to bottom: every agent's answer, every human
// reply, every routing decision, in the order they happened.

import { useContext, useEffect, useId, useMemo, useState, type ReactNode } from "react";

import type * as api from "./api";
import { Fields, Output, Prose } from "./Bits";
import { Cut, loadRecord, RecordNavContext, RecordToggle } from "./Record";
import { clock, duration, money, plural, tokens } from "./format";
import { bytes, entryAttempt, plainText, recId, sameInputs, stepPrompt, textBytes, unrecordedSteps, type Attempt } from "./recordNav";

/** Where the guide explains record. */
const RECORD_GUIDE = "https://simonevellei.com/stiggy/guide.html#record";

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
  const same = useMemo(() => sameInputs(story), [story]);
  const unrecorded = useMemo(() => unrecordedSteps(story), [story]);

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
      {unrecorded > 0 && (
        <p className="muted story-note">
          {unrecorded === 1
            ? "1 agent step keeps no record, so its input isn't shown."
            : `${unrecorded} agent steps keep no record, so their input isn't shown.`}{" "}
          <a href={RECORD_GUIDE} target="_blank" rel="noreferrer">
            See record
          </a>
        </p>
      )}
      <ol className="story-list">
        {shown.map((e, i) => (
          <StoryEntry
            key={entryKey(e, i)}
            e={e}
            attempts={attempts}
            same={same.get(e)}
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
  same,
  future,
  selected,
  onSelect,
}: {
  e: api.Entry;
  attempts: Map<string, Attempt[]>;
  /** The earlier attempt whose input this one repeats. */
  same?: number;
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
          {e.output && e.output.length > 0 && <Output fields={e.output} />}
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
      return <StepEntry e={e} cls={cls} node={node} attempts={attempts} same={same} />;
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

function StepEntry({
  e,
  cls,
  node,
  attempts,
  same,
}: {
  e: api.Entry;
  cls: string;
  node: ReactNode;
  attempts: Map<string, Attempt[]>;
  same?: number;
}) {
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
  const input = at ? e.input : undefined;
  const output = e.output && e.output.length > 0 ? e.output : undefined;
  const plain = plainText(output);

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
      {(input || output || e.error) && (
        <div className="entry-answer entry-box">
          {input && at && (
            <BoxSection label="Input" className="box-input">
              <InputView input={input} at={at} same={same} />
            </BoxSection>
          )}
          {output && (
            <BoxSection label="Output" className="box-output">
              <Output fields={output} />
            </BoxSection>
          )}
          {e.error && (
            <BoxSection label="Error" className="box-error">
              <div className="entry-error">
                <Prose text={e.error} lines={4} />
              </div>
            </BoxSection>
          )}
        </div>
      )}
      {e.outcome === "waiting" && <div className="entry-asks"><Prose text={e.text || "No text."} lines={8} /></div>}
      {e.kept && <p className="entry-line">Kept the approved answer.</p>}
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
          usage={e.usage}
          role={e.role}
          siblings={attempts.get(at.node + "\u0000" + at.key) ?? [at]}
          shownAs={plain !== undefined ? "text" : output ? "fields" : undefined}
          promptAbove={!!input && !input.missing}
        />
      )}
    </li>
  );
}

/** A part of a step's box, named by its label. */
function BoxSection({ label, className, children }: { label: string; className: string; children: ReactNode }) {
  const id = useId();
  return (
    <div className={`box-sec ${className}`} role="group" aria-labelledby={id}>
      <span className="rec-role box-label" id={id}>
        {label}
      </span>
      <div className="box-body">{children}</div>
    </div>
  );
}

/** What the attempt put to its agent: the start of its prompt, the whole
 * of it on request (from the record, through the page's cache), and what
 * came before it in Details. */
function InputView({ input, at, same }: { input: api.InputPreview; at: Attempt; same?: number }) {
  const nav = useContext(RecordNavContext);
  const [unfolded, setUnfolded] = useState(false);
  const [full, setFull] = useState<{ text: string; cut: boolean }>();
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");
  const details = () => nav?.show(at);

  if (input.missing) {
    return input.missing === "too_large" ? (
      <p className="box-note">
        Input too large to keep; see{" "}
        <button type="button" className="link" onClick={details}>
          Details
        </button>
      </p>
    ) : (
      <p className="box-note">Input not kept (record_max_bytes)</p>
    );
  }

  if (same && !unfolded) {
    return (
      <p className="box-note">
        Same as attempt {same}.{" "}
        <button type="button" className="link" onClick={() => setUnfolded(true)}>
          Show it
        </button>
      </p>
    );
  }

  // The preview stops short of a longer prompt the record keeps.
  const more = !!input.cut && input.size > textBytes(input.text);
  const load = () => {
    if (!nav) return;
    setState("loading");
    loadRecord(nav, at)
      .then((r) => {
        const p = r.record ? stepPrompt(r.record)?.msg : undefined;
        if (!p?.text) throw new Error("no prompt");
        setFull({ text: p.text, cut: !!p.truncated });
        setState("idle");
      })
      .catch(() => setState("error"));
  };

  return (
    <>
      {full ? (
        <Prose key="full" text={full.text} lines={3} phoneLines={2} initial />
      ) : (
        <Prose
          text={input.text || "(empty)"}
          lines={3}
          phoneLines={2}
          more={more && nav ? { label: state === "loading" ? "Loading…" : `Show all (${bytes(input.size)})`, onClick: load, busy: state === "loading" } : undefined}
        />
      )}
      {(full ? full.cut : input.cut && !more) && (
        <span className="box-note">
          <Cut />
        </span>
      )}
      {state === "error" && (
        <p className="box-note">
          <span className="entry-error">Could not load the full input.</span>{" "}
          <button type="button" className="link" onClick={load}>
            Try again
          </button>
        </p>
      )}
      {!!input.memory && (
        <button type="button" className="link box-more" onClick={details}>
          + {plural(input.memory, "earlier message")}
        </button>
      )}
    </>
  );
}
