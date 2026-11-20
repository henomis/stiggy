// How a step is set up in its flow, for the side panel next to the trail.

import type * as api from "./api";
import { Prose } from "./Bits";

export function StepConfig({ c }: { c: api.StepConfig }) {
  const facts: string[] = [];
  if (c.max_attempts && c.max_attempts > 1) {
    facts.push(
      `Up to ${c.max_attempts} attempts${c.backoff ? `, ${c.backoff} backoff` : ""}${c.retry_delay ? ` from ${c.retry_delay}` : ""}`,
    );
  }
  if (c.timeout) facts.push(`Times out after ${c.timeout}`);
  if (c.cached) facts.push("Results are cached");
  if (c.schema) facts.push("Answers follow a schema");
  if (c.context?.length) facts.push(`Sees ${c.context.join(", ")}`);
  const writes = Object.entries(c.writes ?? {});

  if (!c.prompt && !c.expected_output && !c.routes?.length && !facts.length && !writes.length) return null;

  return (
    <div className="step-config">
      {c.prompt && (
        <div>
          <span className="config-label">Prompt</span>
          <div className="template">
            <Prose text={c.prompt} lines={6} />
          </div>
        </div>
      )}
      {c.expected_output && (
        <div>
          <span className="config-label">Expected output</span>
          <Prose text={c.expected_output} lines={4} />
        </div>
      )}
      {c.routes && c.routes.length > 0 && (
        <div>
          <span className="config-label">May send work to</span>
          <div>
            {c.routes.map((r) => (
              <span key={r} className="route-chip">
                {r}
              </span>
            ))}
          </div>
        </div>
      )}
      {(writes.length > 0 || facts.length > 0) && (
        <ul className="config-list">
          {writes.map(([k, v]) => (
            <li key={k}>
              Writes <strong>{k}</strong> from <code>{v}</code>
            </li>
          ))}
          {facts.map((f) => (
            <li key={f}>{f}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
