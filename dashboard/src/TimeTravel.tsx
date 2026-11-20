// A slider over the moments of an execution: pick one and the page shows the
// execution as it was right then.

import type * as api from "./api";
import { clock } from "./format";

interface Props {
  moments: api.Moment[];
  /** The selected moment's seq; undefined: now. */
  seq?: number;
  onChange: (seq?: number) => void;
}

export function TimeTravel({ moments, seq, onChange }: Props) {
  if (moments.length < 2) return null;

  const last = moments.length - 1;
  const at = seq === undefined ? last : Math.max(0, moments.findIndex((m) => m.seq === seq));
  const m = moments[at];
  const live = seq === undefined;

  const go = (i: number) => onChange(i >= last ? undefined : moments[i].seq);

  return (
    <div className={`travel${live ? "" : " past"}`}>
      <div className="travel-row">
        <button type="button" className="btn ghost" onClick={() => go(at - 1)} disabled={at === 0} aria-label="One moment back">
          Back
        </button>
        <input
          type="range"
          min={0}
          max={last}
          value={at}
          onChange={(e) => go(Number(e.target.value))}
          aria-label="Moment of the execution"
          aria-valuetext={`${clock(m.time)}: ${m.text}`}
        />
        <button type="button" className="btn ghost" onClick={() => go(at + 1)} disabled={live} aria-label="One moment forward">
          Forward
        </button>
      </div>
      <p className="travel-say" aria-live="polite">
        {live ? (
          <>
            <span className="travel-when">Now.</span> Drag to replay this execution, one moment at a time.
          </>
        ) : (
          <>
            <span className="travel-when">
              Moment {at + 1} of {moments.length}, {clock(m.time)}:
            </span>{" "}
            {m.text}.{" "}
            <button type="button" className="link" onClick={() => onChange(undefined)}>
              Back to now
            </button>
          </>
        )}
      </p>
    </div>
  );
}
