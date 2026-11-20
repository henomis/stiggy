// The pure parts of the record view: which attempt a record belongs to,
// how it is named in a link, what opens by default and what is shown once.

import type * as api from "./api";

/** One attempt of a step: what its record is fetched by. */
export type Attempt = { node: string; key: string; generation: number; attempt: number };

/** Each part encoded, dots included, so the four parts split back apart. */
const part = (s: string) => encodeURIComponent(s).replace(/\./g, "%2E");

/** The attempt as the rec= value of a link: node.key.generation.attempt. */
export function encodeRec(a: Attempt): string {
  return [part(a.node), part(a.key), a.generation, a.attempt].join(".");
}

/** The attempt of a rec= value; undefined when it is not one. */
export function decodeRec(s: string | undefined): Attempt | undefined {
  if (!s) return undefined;
  const p = s.split(".");
  if (p.length !== 4) return undefined;
  const generation = Number(p[2]);
  const attempt = Number(p[3]);
  if (!Number.isInteger(generation) || !Number.isInteger(attempt) || generation < 1 || attempt < 1) return undefined;
  try {
    const node = decodeURIComponent(p[0]);
    const key = decodeURIComponent(p[1]);
    if (!node || !key) return undefined;
    return { node, key, generation, attempt };
  } catch {
    return undefined;
  }
}

/** The id an attempt's open state and cached record are kept under. */
export const recId = encodeRec;

/** The attempt of a story entry, when it has a record. */
export function entryAttempt(e: api.Entry): Attempt | undefined {
  if (e.kind !== "step" || !e.recorded || !e.node || !e.key || !e.generation || !e.attempt) return undefined;
  return { node: e.node, key: e.key, generation: e.generation, attempt: e.attempt };
}

/** The recorded attempts of the same step as a, in story order. */
export function siblings(story: api.Entry[], a: Attempt): Attempt[] {
  const out: Attempt[] = [];
  for (const e of story) {
    const x = entryAttempt(e);
    if (x && x.node === a.node && x.key === a.key) out.push(x);
  }
  return out;
}

/** The recorded attempt a timeline bar stands for: same step and attempt
 * number, the start closest in time (generations do not show on bars). */
export function spanAttempt(story: api.Entry[], s: api.Span): Attempt | undefined {
  let best: Attempt | undefined;
  let gap = Infinity;
  const t = Date.parse(s.start);
  for (const e of story) {
    const x = entryAttempt(e);
    if (!x || x.node !== s.node || x.key !== s.key || x.attempt !== (s.attempt ?? 1)) continue;
    const d = Math.abs(Date.parse(e.time) - t);
    if (d < gap) {
      gap = d;
      best = x;
    }
  }
  return best;
}

/** The record toggle's words, by the attempt's outcome. */
export function toggleLabel(outcome: string | undefined, open: boolean): string {
  if (open) return "Hide";
  switch (outcome) {
    case "failed":
    case "retry":
      return "How it failed";
    case "waiting":
      return "How it got to this question";
    default:
      return "How it got this answer";
  }
}

/** Which nested run each tool call started: children are matched, in
 * order, to the first tool call of the same tool not matched yet. The rest
 * (runs inside the step, or delegates without a matching call) are left. */
export function matchChildren(
  calls: api.RecordCall[],
  children: api.RecordRun[],
): { byCall: Map<api.RecordToolCall, api.RecordRun>; rest: api.RecordRun[] } {
  const tcs = calls.flatMap((c) => c.tool_calls ?? []);
  const byCall = new Map<api.RecordToolCall, api.RecordRun>();
  const rest: api.RecordRun[] = [];
  for (const ch of children) {
    const t = ch.tool ? tcs.find((x) => x.tool === ch.tool && !byCall.has(x)) : undefined;
    if (t) byCall.set(t, ch);
    else rest.push(ch);
  }
  return { byCall, rest };
}

/** A step of the list opens by default when it failed, when one of its
 * tools failed, and when it is the last. */
export function defaultOpen(c: api.RecordCall, last: boolean): boolean {
  return last || !!c.error || (c.tool_calls ?? []).some((t) => !!t.error);
}

/** What each call of the list shows besides its answer and tools: the
 * messages new since the previous call, without those shown already (the
 * previous answer, the results in its tool cards), and the reasoning
 * behind its answer (carried by the next call's copy of that answer). */
export function ledgerParts(calls: api.RecordCall[]): { msgs: api.RecordMessage[]; reasoning?: string }[] {
  const out = calls.map(() => ({ msgs: [] as api.RecordMessage[], reasoning: undefined as string | undefined }));
  calls.forEach((c, i) => {
    if (i === 0) return; // the first call's messages are what was sent
    const prev = calls[i - 1];
    const cards = (prev.tool_calls ?? []).length > 0;
    for (const m of c.messages ?? []) {
      if (m.role === "tool" && cards) continue;
      if (m.role === "assistant") {
        if (m.reasoning) out[i - 1].reasoning = [out[i - 1].reasoning, m.reasoning].filter(Boolean).join("\n\n");
        if (!m.text || m.text === prev.response) continue;
        out[i].msgs.push({ ...m, reasoning: undefined });
        continue;
      }
      out[i].msgs.push(m);
    }
  });
  return out;
}

/** The last thing the model answered in a run. */
export function lastAnswer(run: api.RecordRun): { text: string; cut: boolean } | undefined {
  if (run.output) return { text: run.output, cut: !!run.output_truncated };
  const calls = run.calls ?? [];
  for (let i = calls.length - 1; i >= 0; i--) {
    const c = calls[i];
    if (c.response && !c.compaction) return { text: c.response, cut: !!c.response_truncated };
  }
  return undefined;
}

/**
 * How a run's answer is shown, by where else it already is: "fields" (the
 * story entry shows it as fields), "tool" (the tool card that started the
 * run shows it as the result), or nowhere. Shown once: folded as raw when
 * the fields have it, left out when the tool card has it, open otherwise.
 */
export function answerPlan(
  shownAs: "fields" | "tool" | undefined,
  run: api.RecordRun,
): { kind: "raw" | "open" | "none"; text: string; cut: boolean } {
  const a = lastAnswer(run);
  if (!a || shownAs === "tool") return { kind: "none", text: "", cut: false };
  return { kind: shownAs === "fields" ? "raw" : "open", ...a };
}

/** A size in bytes: 812 B, 1.0 KB, 3.4 MB. */
export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

/** The byte length of a text in UTF-8. */
export const textBytes = (s: string) => new TextEncoder().encode(s).length;
