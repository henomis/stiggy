import { useCallback, useEffect, useMemo, useState } from "react";

import * as api from "./api";
import { ATTACHMENTS, BLOCKS, newAttachment, newBlock } from "./blocks";
import { Canvas, DND_ATTACHMENT, DND_BLOCK } from "./Canvas";
import { tidy } from "./layout";
import { Panel } from "./Panel";
import type { Block, BlockType, Credential, Problem, Project, Workflow } from "./project";
import { newId, newProject, newWorkflow } from "./project";
import { SelectField, TextField } from "./widgets";

const HISTORY = 200;

/** The project with undo and redo. */
function useHistory(initial: Project) {
  const [s, setS] = useState({ past: [] as Project[], now: initial, future: [] as Project[] });
  const set = useCallback((f: (p: Project) => Project) => {
    setS((s) => {
      const now = f(s.now);
      return now === s.now ? s : { past: [...s.past.slice(-HISTORY), s.now], now, future: [] };
    });
  }, []);
  const reset = useCallback((now: Project) => setS({ past: [], now, future: [] }), []);
  const undo = useCallback(() => setS((s) => (s.past.length ? { past: s.past.slice(0, -1), now: s.past[s.past.length - 1], future: [s.now, ...s.future] } : s)), []);
  const redo = useCallback(() => setS((s) => (s.future.length ? { past: [...s.past, s.now], now: s.future[0], future: s.future.slice(1) } : s)), []);
  return { project: s.now, set, reset, undo, redo, canUndo: s.past.length > 0, canRedo: s.future.length > 0 };
}

function download(name: string, text: string, type: string) {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([text], { type }));
  a.download = name;
  a.click();
  URL.revokeObjectURL(a.href);
}

const ENV_HINT: Record<string, string> = { openai: "OPENAI_API_KEY", anthropic: "ANTHROPIC_API_KEY", google: "GEMINI_API_KEY" };

export function App() {
  const h = useHistory(newProject());
  const { project } = h;

  const [catalog, setCatalog] = useState<api.Catalog>(api.emptyCatalog);
  const [file, setFile] = useState<{ path: string; output: string } | null>(null);
  const [saved, setSaved] = useState(JSON.stringify(project));
  const [wfId, setWfId] = useState(project.workflows[0].id);
  const [selected, setSelected] = useState<string | null>(null);
  const [drawer, setDrawer] = useState<"problems" | "yaml" | null>(null);
  const [creds, setCreds] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [search, setSearch] = useState("");

  const dirty = JSON.stringify(project) !== saved;
  const wf = project.workflows.find((w) => w.id === wfId) ?? project.workflows[0];
  const block = wf.nodes.find((n) => n.id === selected) ?? null;

  // Start: the catalog and the project file, if stiggy design was given one.
  useEffect(() => {
    api.catalog().then(setCatalog, (e) => setNotice(`catalog: ${e.message}`));
    api.readProject().then(
      (f) => {
        if (!f) return;
        setFile({ path: f.path, output: f.output });
        const p = f.project ?? newProject(f.path.split("/").pop()!.replace(/\.stiggy\.json$|\.json$/, ""));
        h.reset(p);
        setSaved(f.project ? JSON.stringify(p) : "");
        setWfId(p.workflows[0]?.id);
        setSelected(null);
      },
      (e) => setNotice(`project: ${e.message}`),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Compile as the user works.
  const [compiled, setCompiled] = useState<api.Compiled | null>(null);
  const [checking, setChecking] = useState(false);
  useEffect(() => {
    const ctl = new AbortController();
    setChecking(true);
    const t = setTimeout(() => {
      api
        .compile(project, ctl.signal)
        .then(setCompiled)
        .catch((e) => e.name !== "AbortError" && setCompiled({ ok: false, yaml: "", problems: [{ msg: e.message }] }))
        .finally(() => !ctl.signal.aborted && setChecking(false));
    }, 350);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [project]);

  const problems = compiled?.problems ?? [];

  const errorsByNode = useMemo(() => {
    const m = new Map<string, number>();
    for (const p of problems) if (p.workflow === wf.id && p.node) m.set(p.node, (m.get(p.node) ?? 0) + 1);
    return m;
  }, [problems, wf.id]);

  const setWf = (f: (w: Workflow) => Workflow) =>
    h.set((p) => ({ ...p, workflows: p.workflows.map((w) => (w.id === wf.id ? f(w) : w)) }));

  const setBlock = (b: Block) => setWf((w) => ({ ...w, nodes: w.nodes.map((n) => (n.id === b.id ? b : n)) }));

  const addBlock = (type: BlockType, at?: { x: number; y: number }) => {
    const def = BLOCKS.find((b) => b.type === type)!;
    const existing = def.single ? wf.nodes.find((n) => n.type === type) : undefined;
    if (existing) {
      setSelected(existing.id);
      setNotice(`A workflow has one ${def.label} block.`);
      return;
    }
    // Without a drop point: right of the selected block, below whatever is there.
    const anchor = block ?? wf.nodes.filter((n) => n.type !== "output").at(-1);
    let pos = at ?? { x: (anchor?.position.x ?? 0) + 300, y: anchor?.position.y ?? 120 };
    while (!at && wf.nodes.some((n) => Math.abs(n.position.x - pos.x) < 200 && Math.abs(n.position.y - pos.y) < 80)) {
      pos = { x: pos.x, y: pos.y + 110 };
    }
    const b = newBlock(type, project, pos, wf.nodes.map((n) => n.name));
    setWf((w) => ({ ...w, nodes: [...w.nodes, b] }));
    setSelected(b.id);
  };

  const save = async () => {
    if (!file) {
      download(`${project.name || "project"}.stiggy.json`, JSON.stringify(project, null, 2), "application/json");
      setSaved(JSON.stringify(project));
      return;
    }
    try {
      const res = await api.saveProject(project);
      setSaved(JSON.stringify(project));
      setCompiled(res);
      setNotice(
        res.ok
          ? `Saved ${res.path} and ${res.output}.`
          : `Saved ${res.path} and ${res.output}, but the fleet has problems to fix before it can run.`,
      );
    } catch (e) {
      setNotice(`save: ${(e as Error).message}`);
    }
  };

  // Keyboard: save, undo, redo.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return;
      const editing = (e.target as HTMLElement).closest("input, textarea, select, [contenteditable]");
      if (e.key === "s") {
        e.preventDefault();
        (document.activeElement as HTMLElement | null)?.blur();
        setTimeout(save, 0);
      } else if (e.key.toLowerCase() === "z" && !editing) {
        e.preventDefault();
        if (e.shiftKey) h.redo();
        else h.undo();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => dirty && e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const goTo = (p: Problem) => {
    if (p.workflow) setWfId(p.workflow);
    setSelected(p.node ?? null);
  };

  const q = search.trim().toLowerCase();
  const match = (s: string) => !q || s.toLowerCase().includes(q);

  return (
    <div className="app">
      <header>
        <span className="brand">stiggy</span>
        <div className="project-name">
          <TextField value={project.name} onChange={(name) => h.set((p) => ({ ...p, name }))} />
        </div>
        <button
          className={`status ${compiled?.ok ? "ok" : problems.length ? "bad" : ""}${checking ? " checking" : ""}`}
          onClick={() => setDrawer(drawer === "problems" ? null : "problems")}
        >
          {compiled === null ? "checking…" : compiled.ok ? "✓ ready to run" : `${problems.length} to fix`}
        </button>
        <span className="spacer" />
        <button onClick={() => setCreds(true)}>Providers{project.credentials.length ? ` (${project.credentials.length})` : ""}</button>
        <button onClick={() => setDrawer(drawer === "yaml" ? null : "yaml")}>Fleet YAML</button>
        <button onClick={h.undo} disabled={!h.canUndo} title="Undo (Ctrl+Z)">
          ↶
        </button>
        <button onClick={h.redo} disabled={!h.canRedo} title="Redo (Ctrl+Shift+Z)">
          ↷
        </button>
        <button className="primary" onClick={save} title={file ? `Save ${file.path} and ${file.output} (Ctrl+S)` : "Download the project"}>
          {file ? "Save" : "Download"}
          {dirty && " •"}
        </button>
      </header>

      {notice && (
        <div className="banner">
          {notice}
          <button onClick={() => setNotice(null)}>Dismiss</button>
        </div>
      )}

      <div className="tabs-bar">
        {project.workflows.map((w) => (
          <div
            key={w.id}
            className={`wf-tab${w.id === wf.id ? " on" : ""}`}
            onClick={() => {
              setWfId(w.id);
              setSelected(null);
            }}
            onDoubleClick={() => {
              const name = prompt("Rename workflow", w.name)?.trim();
              if (name) h.set((p) => ({ ...p, workflows: p.workflows.map((x) => (x.id === w.id ? { ...x, name } : x)) }));
            }}
            title="Double-click to rename"
          >
            {w.name}
            {problems.some((p) => p.workflow === w.id) && <span className="dot" />}
            {project.workflows.length > 1 && (
              <button
                className="icon"
                aria-label={`delete ${w.name}`}
                onClick={(e) => {
                  e.stopPropagation();
                  if (!confirm(`Delete the workflow ${w.name}?`)) return;
                  h.set((p) => ({ ...p, workflows: p.workflows.filter((x) => x.id !== w.id) }));
                  if (w.id === wf.id) setWfId(project.workflows.find((x) => x.id !== w.id)!.id);
                }}
              >
                ×
              </button>
            )}
          </div>
        ))}
        <button
          className="wf-add"
          onClick={() => {
            const w = newWorkflow(`Workflow ${project.workflows.length + 1}`);
            h.set((p) => ({ ...p, workflows: [...p.workflows, w] }));
            setWfId(w.id);
            setSelected(null);
          }}
        >
          + Workflow
        </button>
        <span className="spacer" />
        <button className="link" onClick={setWfTidy} title="Arrange the blocks left to right">
          Tidy up
        </button>
      </div>

      <div className="main">
        <aside className="palette">
          <input className="search" placeholder="Search blocks…" value={search} onChange={(e) => setSearch(e.target.value)} />
          {(["Start & end", "AI", "Flow", "Teams"] as const).map((g) => {
            const items = BLOCKS.filter((b) => b.group === g && match(b.label + b.description));
            if (!items.length) return null;
            return (
              <section key={g}>
                <h4>{g}</h4>
                {items.map((b) => (
                  <div
                    key={b.type}
                    className="palette-item"
                    draggable
                    onDragStart={(e) => {
                      e.dataTransfer.setData(DND_BLOCK, b.type);
                      e.dataTransfer.effectAllowed = "copy";
                    }}
                    onClick={() => addBlock(b.type)}
                    title={b.description}
                  >
                    <span className="block-icon" style={{ background: `var(${b.color})` }}>
                      {b.icon}
                    </span>
                    <div>
                      <div className="palette-label">{b.label}</div>
                      <div className="palette-desc">{b.description}</div>
                    </div>
                  </div>
                ))}
              </section>
            );
          })}
          {ATTACHMENTS.some((a) => match(a.label + a.description)) && (
            <section>
              <h4>Tools &amp; knowledge</h4>
              <p className="hint">Drop onto an AI block.</p>
              {ATTACHMENTS.filter((a) => match(a.label + a.description)).map((a) => (
                <div
                  key={a.type}
                  className="palette-item small"
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData(DND_ATTACHMENT, a.type);
                    e.dataTransfer.effectAllowed = "copy";
                  }}
                  onClick={() => {
                    if (block && BLOCKS.find((b) => b.type === block.type)?.ai) {
                      setBlock({ ...block, attachments: [...(block.attachments ?? []), newAttachment(a.type)] });
                    } else {
                      setNotice("Select an AI Agent, AI Router or Loop block first, or drag the tool onto one.");
                    }
                  }}
                  title={a.description}
                >
                  <span className="att-icon">{a.icon}</span>
                  <div>
                    <div className="palette-label">{a.label}</div>
                    <div className="palette-desc">{a.description}</div>
                  </div>
                </div>
              ))}
            </section>
          )}
        </aside>

        <Canvas
          key={wf.id}
          wf={wf}
          selected={selected}
          errors={errorsByNode}
          onSelect={setSelected}
          onChange={(w) => setWf(() => w)}
          onDropBlock={addBlock}
          onAttach={(id, type) => {
            const b = wf.nodes.find((n) => n.id === id)!;
            setBlock({ ...b, attachments: [...(b.attachments ?? []), newAttachment(type)] });
            setSelected(id);
          }}
        />

        <aside className="panel">
          {block ? (
            <Panel
              key={block.id}
              project={project}
              wf={wf}
              block={block}
              catalog={catalog}
              problems={problems.filter((p) => p.workflow === wf.id)}
              onChange={setBlock}
              onRenamePort={(from, to) =>
                setWf((w) => ({
                  ...w,
                  edges: w.edges.map((e) => (e.from.node === block.id && e.from.port === from ? { ...e, from: { ...e.from, port: to } } : e)),
                }))
              }
              onDelete={() => {
                setWf((w) => ({
                  ...w,
                  nodes: w.nodes.filter((n) => n.id !== block.id),
                  edges: w.edges.filter((e) => e.from.node !== block.id && e.to.node !== block.id),
                }));
                setSelected(null);
              }}
              openCredentials={() => setCreds(true)}
            />
          ) : (
            <WorkflowHelp wf={wf} problems={problems.filter((p) => p.workflow === wf.id && !p.node)} file={file} project={project} />
          )}
        </aside>
      </div>

      {drawer && (
        <div className="drawer">
          <div className="drawer-head">
            <button className={drawer === "problems" ? "on" : ""} onClick={() => setDrawer("problems")}>
              To fix {problems.length > 0 && <span className="count">{problems.length}</span>}
            </button>
            <button className={drawer === "yaml" ? "on" : ""} onClick={() => setDrawer("yaml")}>
              Fleet YAML
            </button>
            <span className="spacer" />
            {drawer === "yaml" && compiled?.yaml && (
              <button className="link" onClick={() => download(file?.output.split("/").pop() ?? "fleet.yaml", compiled.yaml, "application/yaml")}>
                Download
              </button>
            )}
            <button className="icon" aria-label="close" onClick={() => setDrawer(null)}>
              ×
            </button>
          </div>
          <div className="drawer-body">
            {drawer === "problems" ? (
              problems.length === 0 ? (
                <div className="empty">{compiled?.ok ? "Nothing to fix: the fleet is ready to run." : "Checking…"}</div>
              ) : (
                <ul className="problems">
                  {problems.map((p, i) => {
                    const w = project.workflows.find((x) => x.id === p.workflow);
                    const n = w?.nodes.find((x) => x.id === p.node);
                    return (
                      <li key={i} onClick={() => goTo(p)}>
                        <span className="where">
                          {w?.name ?? "Project"}
                          {n ? ` › ${n.name}` : ""}
                        </span>
                        {p.msg}
                      </li>
                    );
                  })}
                </ul>
              )
            ) : (
              <pre className="yaml">{compiled?.yaml || "# nothing yet"}</pre>
            )}
          </div>
        </div>
      )}

      {creds && (
        <Credentials
          list={project.credentials}
          catalog={catalog}
          onChange={(credentials) => h.set((p) => ({ ...p, credentials }))}
          onClose={() => setCreds(false)}
        />
      )}
    </div>
  );

  function setWfTidy() {
    tidy(wf).then((w) => setWf(() => w));
  }
}

function WorkflowHelp({
  wf,
  problems,
  file,
  project,
}: {
  wf: Workflow;
  problems: Problem[];
  file: { path: string; output: string } | null;
  project: Project;
}) {
  const start = wf.nodes.find((n) => n.type === "start");
  const inputs = ((start?.params?.inputs as { name: string }[]) ?? []).map((f) => `"${f.name}": "…"`).join(", ");
  const flow = wf.name.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "") || "workflow";
  const ns = project.namespace || project.name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "stiggy";

  return (
    <div className="panel-body">
      <h3>{wf.name}</h3>
      <p className="hint">
        Drag blocks from the left onto the canvas and connect them: from a block's right edge to the next block. Click a block to set it up.
        Delete removes the selected block or connection.
      </p>
      {problems.map((p, i) => (
        <div className="problems-box" key={i}>
          {p.msg}
        </div>
      ))}
      <h4>Run it</h4>
      <p className="hint">Save, then start the fleet and run this workflow:</p>
      <pre className="help">{`stiggy run ${file?.output ?? "fleet.yaml"}
stiggy start -ns ${ns} ${flow} -wait \\
  -input '{${inputs}}'`}</pre>
    </div>
  );
}

function Credentials({
  list,
  catalog,
  onChange,
  onClose,
}: {
  list: Credential[];
  catalog: api.Catalog;
  onChange: (c: Credential[]) => void;
  onClose: () => void;
}) {
  const providers = [...new Set([...catalog.providers, ...catalog.embedder_providers])].sort();
  const set = (i: number, c: Partial<Credential>) => onChange(list.map((x, j) => (j === i ? { ...x, ...c } : x)));

  return (
    <div className="modal-back" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal">
        <div className="modal-head">
          <h3>Model providers</h3>
          <button className="icon" aria-label="close" onClick={onClose}>
            ×
          </button>
        </div>
        <p className="hint">
          The services your AI blocks use, and how to reach them: the kind of provider, the environment variable that holds its API key,
          and optionally its address. Keys are never stored here: the fleet reads them from the environment when it runs.
        </p>
        <div className="cred-list">
          {list.map((c, i) => (
            <div className="cred" key={c.id}>
              <TextField value={c.name} placeholder="name, e.g. OpenAI" onChange={(name) => set(i, { name })} />
              <SelectField
                value={c.provider}
                options={providers.map((p) => ({ value: p, label: p }))}
                onChange={(provider) => set(i, { provider, env: c.env || ENV_HINT[provider] || undefined })}
              />
              <TextField value={c.env} placeholder="API key variable, e.g. OPENAI_API_KEY" onChange={(env) => set(i, { env: env || undefined })} />
              <TextField value={c.base_url} placeholder="address (optional)" onChange={(base_url) => set(i, { base_url: base_url || undefined })} />
              <button className="icon" aria-label="remove" onClick={() => onChange(list.filter((_, j) => j !== i))}>
                ×
              </button>
            </div>
          ))}
        </div>
        <button
          className="link"
          onClick={() => onChange([...list, { id: newId("c"), name: `Provider ${list.length + 1}`, provider: "openai", env: "OPENAI_API_KEY" }])}
        >
          + Add provider
        </button>
      </div>
    </div>
  );
}
