// Form widgets for the settings panel. Text commits on blur, so typing is one
// undo step, not one per key.

import { useEffect, useLayoutEffect, useRef, useState } from "react";

import type { Field, FieldType, Mapping, Ref, Workflow } from "./project";
import { identifier, onlyRef, refToken, splitRefs } from "./project";
import { describe, type Value } from "./refs";

export function Row({ label, hint, error, children }: { label: string; hint?: string; error?: string[]; children: React.ReactNode }) {
  return (
    <div className={`row${error?.length ? " has-error" : ""}`}>
      <label>{label}</label>
      {children}
      {hint && <div className="hint">{hint}</div>}
      {error?.map((e, i) => (
        <div className="error" key={i}>
          {e}
        </div>
      ))}
    </div>
  );
}

export function TextField({
  value,
  onChange,
  placeholder,
  sanitize,
}: {
  value: unknown;
  onChange: (v: string) => void;
  placeholder?: string;
  sanitize?: (s: string) => string;
}) {
  const shown = value === undefined || value === null ? "" : String(value);
  const [text, setText] = useState(shown);
  useEffect(() => setText(shown), [shown]);

  return (
    <input
      type="text"
      value={text}
      placeholder={placeholder}
      spellCheck={false}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => {
        const v = sanitize ? sanitize(text) : text;
        setText(v);
        if (v !== shown) onChange(v);
      }}
      onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
    />
  );
}

export function NumberField({ value, onChange, placeholder }: { value: unknown; onChange: (v: number | undefined) => void; placeholder?: string }) {
  return (
    <TextField
      value={value}
      placeholder={placeholder}
      sanitize={(s) => s.replace(/[^0-9.]/g, "")}
      onChange={(s) => onChange(s === "" ? undefined : Number(s))}
    />
  );
}

export function Check({ checked, onChange, label }: { checked: unknown; onChange: (v: boolean) => void; label: string }) {
  return (
    <label className="check">
      <input type="checkbox" checked={checked === true} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  );
}

export function SelectField({
  value,
  options,
  onChange,
  empty = "Choose…",
}: {
  value: unknown;
  options: { value: string; label: string }[];
  onChange: (v: string) => void;
  empty?: string;
}) {
  const v = typeof value === "string" ? value : "";
  const known = options.some((o) => o.value === v);
  return (
    <select value={v} onChange={(e) => onChange(e.target.value)}>
      <option value="">{empty}</option>
      {!known && v && <option value={v}>{v} (unknown)</option>}
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

/** A menu of values to insert, grouped by where they come from. */
export function InsertMenu({ values, onPick, label = "+ value" }: { values: Value[]; onPick: (r: Ref) => void; label?: string }) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => !box.current?.contains(e.target as Node) && setOpen(false);
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open]);

  const groups = new Map<string, Value[]>();
  for (const v of values) groups.set(v.source, [...(groups.get(v.source) ?? []), v]);

  return (
    <div className="insert" ref={box}>
      <button type="button" className="link" onMouseDown={(e) => e.preventDefault()} onClick={() => setOpen(!open)}>
        {label}
      </button>
      {open && (
        <div className="menu">
          {values.length === 0 && <div className="menu-empty">Nothing yet: connect blocks before this one, or add Start inputs.</div>}
          {[...groups].map(([source, vs]) => (
            <div key={source}>
              <div className="menu-head">{source}</div>
              {vs.map((v) => (
                <button
                  type="button"
                  key={refToken(v.ref)}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => {
                    onPick(v.ref);
                    setOpen(false);
                  }}
                >
                  {v.label}
                  {v.type && <span className="type">{v.type}</span>}
                </button>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function chip(wf: Workflow, r: Ref): HTMLSpanElement {
  const d = describe(wf, r);
  const s = document.createElement("span");
  s.className = `chip${d.missing ? " missing" : ""}`;
  s.contentEditable = "false";
  s.dataset.token = refToken(r);
  s.textContent = d.text;
  return s;
}

function serialize(el: Node): string {
  let out = "";
  el.childNodes.forEach((c) => {
    if (c.nodeType === Node.TEXT_NODE) out += c.nodeValue ?? "";
    else if (c instanceof HTMLElement && c.dataset.token) out += c.dataset.token;
    else if (c.nodeName === "BR") out += "\n";
    else if (c.nodeName === "DIV" || c.nodeName === "P") out += (out && !out.endsWith("\n") ? "\n" : "") + serialize(c);
    else out += serialize(c);
  });
  return out;
}

/**
 * Text with references shown as chips: prompts, instructions, task texts.
 * The value keeps {{ref …}} tokens; the editor shows what they point at.
 */
export function RefEditor({
  value,
  onChange,
  values,
  wf,
  placeholder,
  multiline = true,
}: {
  value: unknown;
  onChange: (v: string) => void;
  values: Value[];
  wf: Workflow;
  placeholder?: string;
  multiline?: boolean;
}) {
  const el = useRef<HTMLDivElement>(null);
  const last = useRef<string | null>(null);
  const range = useRef<Range | null>(null);
  const text = typeof value === "string" ? value : "";

  // Render from the value, unless it is what the editor itself produced.
  useLayoutEffect(() => {
    const e = el.current;
    if (!e || text === last.current) return;
    e.replaceChildren(...splitRefs(text).map((p) => (typeof p === "string" ? document.createTextNode(p) : chip(wf, p))));
    last.current = text;
  }, [text, wf]);

  const commit = () => {
    if (!el.current) return;
    const v = serialize(el.current);
    if (v !== last.current) {
      last.current = v;
      onChange(v);
    }
  };

  const remember = () => {
    const s = window.getSelection();
    if (s && s.rangeCount && el.current?.contains(s.anchorNode)) range.current = s.getRangeAt(0).cloneRange();
  };

  const insert = (r: Ref) => {
    const e = el.current;
    if (!e) return;
    let at = range.current;
    if (!at || !e.contains(at.startContainer)) {
      at = document.createRange();
      at.selectNodeContents(e);
      at.collapse(false);
    }
    at.deleteContents();
    const c = chip(wf, r);
    const after = document.createTextNode("");
    at.insertNode(after);
    at.insertNode(c);
    const s = window.getSelection();
    const caret = document.createRange();
    caret.setStartAfter(c);
    caret.collapse(true);
    s?.removeAllRanges();
    s?.addRange(caret);
    range.current = caret.cloneRange();
    commit();
  };

  return (
    <div className="ref-field">
      <div
        ref={el}
        className={`ref-editor${multiline ? " multi" : ""}`}
        contentEditable
        suppressContentEditableWarning
        data-placeholder={placeholder}
        spellCheck={false}
        onKeyUp={remember}
        onMouseUp={remember}
        onBlur={() => {
          remember();
          commit();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            if (multiline) document.execCommand("insertText", false, "\n");
            else (e.target as HTMLElement).blur();
          }
        }}
        onPaste={(e) => {
          e.preventDefault();
          document.execCommand("insertText", false, e.clipboardData.getData("text/plain"));
        }}
      />
      <InsertMenu values={values} onPick={insert} />
    </div>
  );
}

/** One value: a reference shown as a chip, or plain text. */
export function ValuePick({
  value,
  onChange,
  values,
  wf,
  refsOnly,
  placeholder,
}: {
  value: unknown;
  onChange: (v: string) => void;
  values: Value[];
  wf: Workflow;
  refsOnly?: boolean;
  placeholder?: string;
}) {
  const r = onlyRef(value);
  if (r) {
    const d = describe(wf, r);
    return (
      <div className="value-pick">
        <span className={`chip${d.missing ? " missing" : ""}`}>
          {d.text}
          <button type="button" aria-label="clear" onClick={() => onChange("")}>
            ×
          </button>
        </span>
      </div>
    );
  }
  return (
    <div className="value-pick">
      {!refsOnly && <TextField value={value} onChange={onChange} placeholder={placeholder ?? "text, or pick a value →"} />}
      <InsertMenu values={values} onPick={(x) => onChange(refToken(x))} label={refsOnly ? "Choose a value…" : "+ value"} />
    </div>
  );
}

const TYPES: { value: FieldType; label: string }[] = [
  { value: "text", label: "Text" },
  { value: "number", label: "Number" },
  { value: "boolean", label: "Yes/no" },
  { value: "list", label: "List" },
  { value: "object", label: "Object" },
];

/** Named, typed fields: workflow inputs, structured answers. */
export function FieldList({ value, onChange, add = "Add field" }: { value: unknown; onChange: (v: Field[]) => void; add?: string }) {
  const list = Array.isArray(value) ? (value as Field[]) : [];
  const set = (i: number, f: Partial<Field>) => onChange(list.map((x, j) => (j === i ? { ...x, ...f } : x)));

  return (
    <div className="list">
      {list.map((f, i) => (
        <div className="list-row" key={i}>
          <TextField value={f.name} placeholder="name" sanitize={identifier} onChange={(name) => set(i, { name })} />
          <select value={f.type ?? "text"} onChange={(e) => set(i, { type: e.target.value as FieldType })}>
            {TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
          <TextField value={f.description} placeholder="description" onChange={(description) => set(i, { description: description || undefined })} />
          <button type="button" className="icon" aria-label="remove" onClick={() => onChange(list.filter((_, j) => j !== i))}>
            ×
          </button>
        </div>
      ))}
      <button type="button" className="link" onClick={() => onChange([...list, { name: `field_${list.length + 1}`, type: "text" }])}>
        + {add}
      </button>
    </div>
  );
}

/** Named values: outputs, inputs of another workflow. */
export function MappingList({
  value,
  onChange,
  values,
  wf,
  fixedNames,
}: {
  value: unknown;
  onChange: (v: Mapping[]) => void;
  values: Value[];
  wf: Workflow;
  /** When given, the names are these and cannot be edited. */
  fixedNames?: string[];
}) {
  const list = Array.isArray(value) ? (value as Mapping[]) : [];
  const rows = fixedNames ? fixedNames.map((n) => list.find((m) => m.name === n) ?? { name: n, value: "" }) : list;
  const set = (i: number, m: Partial<Mapping>) => onChange(rows.map((x, j) => (j === i ? { ...x, ...m } : x)));

  return (
    <div className="list">
      {rows.map((m, i) => (
        <div className="list-row mapping" key={fixedNames ? m.name : i}>
          {fixedNames ? (
            <code className="fixed-name">{m.name}</code>
          ) : (
            <TextField value={m.name} placeholder="name" sanitize={identifier} onChange={(name) => set(i, { name })} />
          )}
          <ValuePick value={m.value} onChange={(v) => set(i, { value: v })} values={values} wf={wf} />
          {!fixedNames && (
            <button type="button" className="icon" aria-label="remove" onClick={() => onChange(rows.filter((_, j) => j !== i))}>
              ×
            </button>
          )}
        </div>
      ))}
      {!fixedNames && (
        <button type="button" className="link" onClick={() => onChange([...rows, { name: `value_${rows.length + 1}`, value: "" }])}>
          + Add value
        </button>
      )}
    </div>
  );
}

/** A list of short strings (allow-lists, branch names). */
export function StringList({ value, onChange, placeholder, sanitize }: { value: unknown; onChange: (v: string[]) => void; placeholder?: string; sanitize?: (s: string) => string }) {
  const list = Array.isArray(value) ? (value as string[]) : [];
  return (
    <div className="list">
      {list.map((s, i) => (
        <div className="list-row" key={i}>
          <TextField value={s} placeholder={placeholder} sanitize={sanitize} onChange={(v) => onChange(list.map((x, j) => (j === i ? v : x)))} />
          <button type="button" className="icon" aria-label="remove" onClick={() => onChange(list.filter((_, j) => j !== i))}>
            ×
          </button>
        </div>
      ))}
      <button type="button" className="link" onClick={() => onChange([...list, ""])}>
        + Add
      </button>
    </div>
  );
}
