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

/** How long ago t was, relative to now: "just now", "4m ago", "yesterday". */
export function ago(t: string | undefined, now = Date.now()): string {
  if (!t) return "";
  const ms = now - Date.parse(t);
  if (ms < 30_000) return "just now";
  if (ms < 86_400_000) return duration(ms).split(" ")[0] + " ago";
  if (ms < 2 * 86_400_000) return "yesterday";
  return new Date(t).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

export function clock(t: string): string {
  return new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
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
