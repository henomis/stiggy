// What the agents of an execution do right now: their words as they write
// them, the tools they call, their task lists.

import { useEffect, useRef, useState } from "react";

import * as api from "./api";
import { clock } from "./format";

interface Entry {
  id: number;
  kind: "text" | "tool" | "todo" | "compaction";
  agent: string;
  node: string;
  time: string;
  text: string;
  tool?: string;
  args?: string;
  done?: boolean;
  failed?: boolean;
  todos?: api.Step["todos"];
}

const KEEP = 150;

/** Folds a step into the feed: text chunks of one attempt join up. */
export function fold(feed: Entry[], s: api.Step, id: number): Entry[] {
  const last = feed[feed.length - 1];
  const base = { id, agent: s.agent, node: s.node, time: s.time, text: "" };
  switch (s.type) {
    case "text":
      if (last && last.kind === "text" && last.node === s.node && last.agent === s.agent) {
        return [...feed.slice(0, -1), { ...last, text: last.text + (s.text ?? "") }];
      }
      return [...feed, { ...base, kind: "text" as const, text: s.text ?? "" }].slice(-KEEP);
    case "tool_call":
      return [...feed, { ...base, kind: "tool" as const, tool: s.tool, args: s.args }].slice(-KEEP);
    case "tool_result": {
      for (let i = feed.length - 1; i >= 0; i--) {
        const e = feed[i];
        if (e.kind === "tool" && e.tool === s.tool && !e.done) {
          const next = [...feed];
          next[i] = { ...e, done: true, failed: s.is_error };
          return next;
        }
      }
      return feed;
    }
    case "todo":
      return [...feed.filter((e) => !(e.kind === "todo" && e.node === s.node)), { ...base, kind: "todo" as const, todos: s.todos }].slice(-KEEP);
    case "compaction":
      return [...feed, { ...base, kind: "compaction" as const, text: s.is_error ? `Compaction failed: ${s.text ?? ""}` : "Compacted the conversation" }].slice(-KEEP);
    default:
      return feed;
  }
}

export function LiveSteps({ ns, id, live }: { ns: string; id: string; live: boolean }) {
  const [feed, setFeed] = useState<Entry[]>([]);
  const [open, setOpen] = useState(false);
  const n = useRef(0);
  const box = useRef<HTMLOListElement>(null);
  const pinned = useRef(true);

  useEffect(() => {
    if (!live) return;
    setOpen(true);
    const close = api.stream<api.Step>(
      api.progressPath(ns, id),
      "step",
      (s) => setFeed((f) => fold(f, s, ++n.current)),
      () => setOpen(false),
    );
    return () => {
      close();
      setOpen(false);
    };
  }, [ns, id, live]);

  useEffect(() => {
    const el = box.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [feed]);

  if (!live && feed.length === 0) return null;

  return (
    <div className="live">
      <div className="live-state">
        <span className={`live-dot${open ? " on" : ""}`} aria-hidden />
        {open ? "Following the agents as they work" : "The execution stopped streaming"}
      </div>
      {feed.length === 0 ? (
        <p className="muted">
          Nothing streamed yet. Agent steps show their words and tool calls here while they run; earlier steps are in the
          timeline.
        </p>
      ) : (
        <ol
          className="feed"
          ref={box}
          onScroll={(e) => {
            const el = e.currentTarget;
            pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
          }}
          aria-live="polite"
        >
          {feed.map((e) => (
            <li key={e.id} className={`feed-${e.kind}`}>
              <div className="feed-who">
                <span>{e.agent}</span>
                <span className="muted small">
                  {e.node}, {clock(e.time)}
                </span>
              </div>
              {e.kind === "text" && <p className="feed-text">{e.text}</p>}
              {e.kind === "tool" && (
                <p className={`tool-call${e.done ? (e.failed ? " failed" : " done") : " pending"}`}>
                  <span className="tool-name">{e.tool}</span>
                  {e.args && <span className="tool-args">{e.args.length > 140 ? e.args.slice(0, 139) + "…" : e.args}</span>}
                  <span className="tool-state">{e.done ? (e.failed ? "failed" : "returned") : "calling"}</span>
                </p>
              )}
              {e.kind === "todo" && (
                <ul className="todos">
                  {(e.todos ?? []).map((t, i) => (
                    <li key={i} className={`todo-${t.status ?? "pending"}`}>
                      {t.content ?? t.text}
                    </li>
                  ))}
                </ul>
              )}
              {e.kind === "compaction" && <p className="muted small">{e.text}</p>}
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
