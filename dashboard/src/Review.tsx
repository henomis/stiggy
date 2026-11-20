// Answering a step that waits for a human: a question to answer, or an
// answer to approve or send back with feedback.

import { useState } from "react";

import * as api from "./api";
import { Prose } from "./Bits";
import { ago } from "./format";

const BY_KEY = "stiggy-dashboard-by";

function rememberedName(): string {
  try {
    return localStorage.getItem(BY_KEY) ?? "";
  } catch {
    return "";
  }
}

function remember(name: string) {
  try {
    localStorage.setItem(BY_KEY, name);
  } catch {
    // Private windows have no storage; the name is just not remembered.
  }
}

interface Props {
  ns: string;
  id: string;
  wait: api.Wait;
  readOnly: boolean;
  compact?: boolean;
  onDone: () => void;
}

export function Review({ ns, id, wait, readOnly, compact, onDone }: Props) {
  const [text, setText] = useState("");
  const [by, setBy] = useState(rememberedName);
  const [mode, setMode] = useState<"idle" | "feedback">("idle");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [sent, setSent] = useState<string>();
  const review = wait.kind === "review";

  const send = async (reply: Omit<api.Reply, "node" | "by">, what: string) => {
    setBusy(true);
    setError(undefined);
    try {
      if (by) remember(by);
      await api.resume(ns, id, { node: wait.node, by: by || undefined, ...reply });
      setSent(what);
      setText("");
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <article className={`review${compact ? " compact" : ""}`}>
      <header className="review-head">
        <span className="review-kind">{review ? "Review the answer" : "Answer the question"}</span>
        <span className="muted">
          {wait.agent ? `${wait.agent} at ${wait.node}` : wait.node}
          {wait.asked_at ? `, asked ${ago(wait.asked_at)}` : ""}
        </span>
      </header>

      {wait.turns && wait.turns.length > 0 && !compact && (
        <ol className="turns">
          {wait.turns.map((t, i) => (
            <li key={i}>
              <Prose text={t.text} lines={3} />
              <p className="turn-reply">{t.reply ? `Replied: ${t.reply}` : "Approved"}</p>
            </li>
          ))}
        </ol>
      )}

      <div className="review-text">
        <Prose text={wait.text || "The agent left no text."} lines={compact ? 4 : 12} />
      </div>

      {review && wait.route && (
        <p className="muted review-route">
          Approving continues to <strong>{wait.route}</strong>
          {wait.route_reason ? `: ${wait.route_reason}` : ""}
        </p>
      )}

      {sent ? (
        <p className="review-sent" role="status">
          {sent}. The step continues.
        </p>
      ) : readOnly ? (
        <p className="muted">This dashboard is read-only. Answer with `stiggy resume`.</p>
      ) : (
        <form
          className="review-form"
          onSubmit={(e) => {
            e.preventDefault();
            if (!review) send({ answer: text }, "Answer sent");
            else if (mode === "feedback") send({ feedback: text }, "Changes requested");
          }}
        >
          {(!review || mode === "feedback") && (
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={compact ? 2 : 4}
              placeholder={review ? "What should change?" : "Your answer"}
              aria-label={review ? "Feedback" : "Answer"}
              autoFocus={mode === "feedback"}
            />
          )}
          <div className="review-actions">
            {review && mode === "idle" && (
              <>
                <button type="button" className="btn primary" disabled={busy} onClick={() => send({ approve: true }, "Approved")}>
                  Approve
                </button>
                <button type="button" className="btn" disabled={busy} onClick={() => setMode("feedback")}>
                  Ask for changes
                </button>
              </>
            )}
            {review && mode === "feedback" && (
              <>
                <button type="submit" className="btn primary" disabled={busy || !text.trim()}>
                  Send feedback
                </button>
                <button type="button" className="btn ghost" onClick={() => setMode("idle")}>
                  Back
                </button>
              </>
            )}
            {!review && (
              <button type="submit" className="btn primary" disabled={busy || !text.trim()}>
                Send answer
              </button>
            )}
            <label className="by">
              <span>as</span>
              <input value={by} onChange={(e) => setBy(e.target.value)} placeholder="your name" size={12} />
            </label>
          </div>
          {error && (
            <p className="error" role="alert">
              {error}
            </p>
          )}
        </form>
      )}
    </article>
  );
}
