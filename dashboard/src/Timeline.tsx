// The execution in time: one lane per step, one bar per attempt, and the
// time a human took to answer drawn as its own bar.

import type { Entry, Timeline as TL } from "./api";
import { duration, money, tokens } from "./format";
import { spanAttempt, type Attempt } from "./recordNav";

const OUTCOME_WORDS: Record<string, string> = {
  running: "running",
  done: "done",
  cached: "from cache",
  retry: "failed, retried",
  failed: "failed",
  waiting: "paused for a human",
  human: "waiting for a human",
  cancelled: "cancelled",
};

export function Timeline({
  tl,
  now,
  selected,
  onSelect,
  story,
  onOpen,
}: {
  tl: TL;
  now: number;
  selected?: string;
  onSelect: (n: string) => void;
  /** With onOpen, the bar of an attempt with a record opens it. */
  story?: Entry[];
  onOpen?: (at: Attempt) => void;
}) {
  if (!tl.spans.length) return <p className="muted">No steps ran yet.</p>;

  const t0 = Date.parse(tl.start);
  const t1 = Math.max(tl.end ? Date.parse(tl.end) : now, t0 + 1000);
  const total = t1 - t0;
  const pct = (t: number) => ((t - t0) / total) * 100;
  const lanes: string[] = [];
  for (const s of tl.spans) if (!lanes.includes(s.node)) lanes.push(s.node);
  const ticks = [0, 0.25, 0.5, 0.75, 1];

  return (
    <div className="timeline" role="table" aria-label="Steps over time">
      <div className="tl-axis" role="row" aria-hidden>
        <span className="tl-lane-name" />
        <span className="tl-track">
          {ticks.map((f) => (
            <span key={f} className="tl-tick" style={{ left: `${f * 100}%` }}>
              {f === 0 ? "start" : "+" + duration(total * f)}
            </span>
          ))}
        </span>
      </div>
      {lanes.map((lane) => (
        <div key={lane} className={`tl-row${selected === lane ? " selected" : ""}`} role="row">
          <button type="button" className="tl-lane-name" role="rowheader" onClick={() => onSelect(lane)}>
            {lane}
          </button>
          <span className="tl-track" role="cell">
            {tl.spans
              .filter((s) => s.node === lane)
              .map((s, i) => {
                const a = Date.parse(s.start);
                const b = s.end ? Date.parse(s.end) : now;
                const parts = [
                  `${lane}${s.attempt && s.attempt > 1 ? `, attempt ${s.attempt}` : ""}: ${OUTCOME_WORDS[s.outcome] ?? s.outcome}`,
                  duration(b - a),
                ];
                if (s.by) parts.push(`answered by ${s.by}`);
                if (s.tokens) parts.push(`${tokens(s.tokens)} tokens`);
                if (s.cost_usd) parts.push(money(s.cost_usd));
                if (s.next) parts.push(`went to ${s.next}`);
                if (s.error) parts.push(s.error);
                const cls = `tl-bar o-${s.outcome}${s.end ? "" : " open"}`;
                const style = { left: `${pct(a)}%`, width: `max(3px, ${pct(b) - pct(a)}%)` };
                const at = onOpen && story && s.outcome !== "human" ? spanAttempt(story, s) : undefined;
                if (at) {
                  return (
                    <button
                      key={i}
                      type="button"
                      className={cls + " has-rec"}
                      style={style}
                      title={parts.join("\n") + "\nClick to show its record"}
                      aria-label={`${parts[0]}, show its record`}
                      onClick={() => {
                        onSelect(lane);
                        onOpen!(at);
                      }}
                    />
                  );
                }
                return <span key={i} className={cls} style={style} title={parts.join("\n")} />;
              })}
            {tl.marks
              .filter((m) => m.node === lane)
              .map((m, i) => (
                <span key={"m" + i} className="tl-mark" style={{ left: `${pct(Date.parse(m.time))}%` }} title={m.text} />
              ))}
          </span>
        </div>
      ))}
      <div className="tl-legend" aria-hidden>
        <span className="o-done">done</span>
        <span className="o-retry">retried</span>
        <span className="o-failed">failed</span>
        <span className="o-human">waiting for a human</span>
        <span className="o-running">running</span>
      </div>
    </div>
  );
}
