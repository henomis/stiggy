// Small pieces shared by the pages: status marks, charts, meters, fields.

import { useState, type ReactNode } from "react";

import type { Bucket, Field, Share, Status } from "./api";
import { money, statusWord, tokens } from "./format";
import { plainText } from "./recordNav";

export function StatusMark({ status, label = true }: { status: Status | string; label?: boolean }) {
  return (
    <span className={`status st-${status}`}>
      <span className="status-dot" aria-hidden />
      {label ? statusWord(status) : <span className="sr-only">{statusWord(status)}</span>}
    </span>
  );
}

/** Executions started per hour over the last day, failures in carmine. */
export function Throughput({ buckets }: { buckets: Bucket[] }) {
  const max = Math.max(1, ...buckets.map((b) => b.started));
  const w = 6;
  const gap = 2;
  const h = 40;
  const total = buckets.reduce((n, b) => n + b.started, 0);
  return (
    <svg
      className="throughput"
      viewBox={`0 0 ${buckets.length * (w + gap)} ${h + 1}`}
      role="img"
      aria-label={`${total} started in the last 24 hours`}
    >
      <line className="baseline" x1={0} x2={buckets.length * (w + gap)} y1={h + 0.5} y2={h + 0.5} />
      {buckets.map((b, i) => {
        const bh = b.started ? Math.max(2, (b.started / max) * h) : 0;
        const fh = b.failed ? Math.max(2, (Math.min(b.failed, b.started || b.failed) / max) * h) : 0;
        const hour = new Date(b.hour).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
        return (
          <g key={b.hour}>
            <title>{`${hour}: ${b.started} started, ${b.finished} finished, ${b.failed} failed`}</title>
            <rect className="bar" x={i * (w + gap)} y={h - bh} width={w} height={bh} rx={1.5} />
            {fh > 0 && <rect className="bar-failed" x={i * (w + gap)} y={h - fh} width={w} height={fh} rx={1.5} />}
          </g>
        );
      })}
    </svg>
  );
}

/** A share of a whole: who spent the tokens and the money. */
export function ShareBars({ shares, by }: { shares: Share[]; by: "cost" | "tokens" }) {
  const val = (s: Share) => (by === "cost" ? s.cost_usd : s.tokens_in + s.tokens_out);
  const max = Math.max(...shares.map(val), 0) || 1;
  return (
    <ul className="shares">
      {shares.map((s) => (
        <li key={s.name}>
          <span className="share-name">{s.name}</span>
          <span className="share-track">
            <span className="share-fill" style={{ width: `${(val(s) / max) * 100}%` }} />
          </span>
          <span className="share-val num">{by === "cost" ? money(s.cost_usd) : tokens(val(s))}</span>
        </li>
      ))}
    </ul>
  );
}

export function Meter({ label, used, limit, format }: { label: string; used: number; limit: number; format: (n: number) => string }) {
  const f = limit > 0 ? Math.min(1, used / limit) : 0;
  const tone = f >= 0.9 ? "hot" : f >= 0.7 ? "warm" : "";
  return (
    <div className={`meter ${tone}`}>
      <div className="meter-head">
        <span>{label}</span>
        <span className="num">
          {format(used)} <span className="muted">of {format(limit)}</span>
        </span>
      </div>
      <div className="meter-track" role="meter" aria-valuemin={0} aria-valuemax={limit} aria-valuenow={used} aria-label={label}>
        <div className="meter-fill" style={{ width: `${f * 100}%` }} />
      </div>
    </div>
  );
}

/** Input or output, as labelled values; long text reads as prose. */
export function Fields({ fields, empty }: { fields: Field[]; empty: string }) {
  if (!fields.length) return <p className="muted">{empty}</p>;
  return (
    <dl className="fields">
      {fields.map((f) => (
        <FieldRow key={f.key} f={f} />
      ))}
    </dl>
  );
}

function FieldRow({ f }: { f: Field }) {
  const [open, setOpen] = useState(false);
  const nested = f.items && f.items.length > 0;
  const long = f.kind === "text" && f.text.length > 160;
  return (
    <div className={`field k-${f.kind}`}>
      <dt>{f.key}</dt>
      <dd>
        {nested ? (
          <>
            <button type="button" className="link" aria-expanded={open} onClick={() => setOpen(!open)}>
              {open ? "Hide" : "Show"} {f.text}
            </button>
            {open && (
              <dl className="fields nested">
                {f.items!.map((c) => (
                  <FieldRow key={c.key} f={c} />
                ))}
              </dl>
            )}
          </>
        ) : long ? (
          <Prose text={f.text} />
        ) : (
          <span className={f.kind === "number" ? "num" : undefined}>{f.text}</span>
        )}
      </dd>
    </div>
  );
}

/** Long text, folded after a few lines (phoneLines on a phone); open set
 * shows it all (as after "Show all"), so the browser's find sees every
 * line. With more, the text is the start of a longer one: its button asks
 * for the rest instead of unfolding. */
export function Prose({
  text,
  lines = 6,
  phoneLines,
  open: all,
  initial = false,
  more,
}: {
  text: string;
  lines?: number;
  phoneLines?: number;
  open?: boolean;
  /** Starts unfolded. */
  initial?: boolean;
  more?: { label: string; onClick: () => void; busy?: boolean };
}) {
  const [mine, setOpen] = useState(initial);
  const open = all || mine;
  const many = text.split("\n").length > Math.min(lines, phoneLines ?? lines) || text.length > 600;
  const style = { ["--lines" as string]: lines, ...(phoneLines ? { ["--lines-phone" as string]: phoneLines } : {}) };
  return (
    <div className="prose-wrap">
      <div className={`prose${(many || more) && !open ? " folded" : ""}`} style={style}>
        {text}
      </div>
      {more && !open ? (
        <button type="button" className="link" disabled={more.busy} onClick={more.onClick}>
          {more.label}
        </button>
      ) : (
        many &&
        !all && (
          <button type="button" className="link" onClick={() => setOpen(!open)}>
            {open ? "Show less" : "Show all"}
          </button>
        )
      )}
    </div>
  );
}

/** A step's or an execution's output: a plain answer (one text field named
 * "text") as text, anything else as labelled values. */
export function Output({ fields, empty = "", lines = 8 }: { fields: Field[]; empty?: string; lines?: number }) {
  const text = plainText(fields);
  if (text !== undefined) return <Prose text={text || "(empty)"} lines={lines} />;
  return <Fields fields={fields} empty={empty} />;
}

export function Section({ title, aside, children, className }: { title: string; aside?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`section ${className ?? ""}`}>
      <header className="section-head">
        <h2>{title}</h2>
        {aside}
      </header>
      {children}
    </section>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}
