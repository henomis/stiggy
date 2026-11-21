// Numbers, money, durations and times, as people read them.

export function tokens(n: number): string {
  if (!n) return "0";
  if (n < 1000) return String(Math.round(n));
  if (n < 1e6) return trim(n / 1000) + "k";
  return trim(n / 1e6) + "M";
}

function trim(n: number): string {
  return (n >= 100 ? n.toFixed(0) : n >= 10 ? n.toFixed(1) : n.toFixed(2)).replace(/\.?0+$/, "");
}

export function money(usd: number): string {
  if (!usd) return "$0";
  if (usd < 0.01) return "<$0.01";
  if (usd < 100) return "$" + usd.toFixed(2);
  return "$" + Math.round(usd).toLocaleString("en-US");
}

/** A duration in milliseconds: 850ms, 12s, 4m 10s, 2h 05m, 3d 4h. */
export function duration(ms: number): string {
  if (ms < 0) ms = 0;
  if (ms < 1000) return Math.round(ms) + "ms";
  if (ms < 10_000) return (ms / 1000).toFixed(1).replace(/\.0$/, "") + "s";
  const s = Math.floor(ms / 1000);
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${String(m % 60).padStart(2, "0")}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

const TIME: Intl.DateTimeFormatOptions = { hour: "2-digit", minute: "2-digit", second: "2-digit" };

/** The time of day of t, with seconds, in the viewer's locale. */
const timeOf = (t: string) => new Date(t).toLocaleTimeString(undefined, TIME);

/** A date with its time, to the second: "12 Nov 2026, 14:03:22". */
export function stamp(t: string): string {
  const d = new Date(t);
  return `${d.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" })}, ${timeOf(t)}`;
}

/** How long ago t was, relative to now: "just now", "4m ago", "yesterday, 14:03:22", "12 Nov 2026, 14:03:22". */
export function ago(t: string | undefined, now = Date.now()): string {
  if (!t) return "";
  const ms = now - Date.parse(t);
  if (ms < 30_000) return "just now";
  if (ms < 86_400_000) return duration(ms).split(" ")[0] + " ago";
  if (ms < 2 * 86_400_000) return `yesterday, ${timeOf(t)}`;
  return stamp(t);
}

/** The time of t, with seconds; with its date when it is not today. */
export function clock(t: string, now = Date.now()): string {
  const d = new Date(t);
  if (d.toDateString() === new Date(now).toDateString()) return timeOf(t);
  return `${d.toLocaleDateString(undefined, { day: "numeric", month: "short" })}, ${timeOf(t)}`;
}

/** The visible part of an execution id: long generated ids keep their tail. */
export function shortId(id: string): string {
  return id.length > 14 ? "…" + id.slice(-10) : id;
}

export function plural(n: number, one: string, many = one + "s"): string {
  return `${n} ${n === 1 ? one : many}`;
}

const STATUS_WORDS: Record<string, string> = {
  running: "Running",
  waiting: "Waiting",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
  quarantined: "Quarantined",
};

export function statusWord(s: string): string {
  return STATUS_WORDS[s] ?? s;
}

/** Fields in one line: "topic: ants, n: 3", clipped to max characters. */
export function preview(fields: { key: string; text: string; kind?: string }[], max = 140): string {
  // Text says most, then numbers and booleans; nested values last.
  const rank = (k?: string) => (k === "text" ? 0 : k === "list" || k === "object" ? 2 : 1);
  const line = [...fields]
    .sort((a, b) => rank(a.kind) - rank(b.kind))
    .map((f) => {
      const text = f.text.replace(/\s+/g, " ").trim();
      return f.key === "value" && fields.length === 1 ? text : `${f.key}: ${text}`;
    })
    .join(", ");
  const chars = [...line];
  return chars.length > max ? chars.slice(0, max - 1).join("") + "…" : line;
}
