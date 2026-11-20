import { useCallback, useEffect, useRef, useState } from "react";

import * as api from "./api";
import { decodeRec, encodeRec, type Attempt } from "./recordNav";

/** Polls load every ms while the page is visible; refresh() reloads now. */
export function usePoll<T>(load: () => Promise<T>, ms: number, deps: unknown[]) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<string>();
  const [at, setAt] = useState<number>();
  const loadRef = useRef(load);
  loadRef.current = load;
  const seq = useRef(0);

  const refresh = useCallback(() => {
    const n = ++seq.current;
    loadRef
      .current()
      .then((d) => {
        if (n !== seq.current) return;
        setData(d);
        setError(undefined);
        setAt(Date.now());
      })
      .catch((e: unknown) => {
        if (n === seq.current) setError(e instanceof Error ? e.message : String(e));
      });
  }, []);

  useEffect(() => {
    setData(undefined);
    refresh();
    const t = setInterval(() => {
      if (document.visibilityState === "visible") refresh();
    }, ms);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { data, error, at, refresh };
}

/** The route, from the location hash: #/ns/<ns>/x/<id>, with
 * ?rec=<node>.<key>.<generation>.<attempt> for an attempt's open record. */
export type Route =
  | { page: "overview" }
  | { page: "namespace"; ns: string; flow?: string; status?: string }
  | { page: "execution"; ns: string; id: string; rec?: Attempt };

/** The rec= value of a query, as written: its parts carry their own
 * encoding, which URLSearchParams would undo. */
const rawRec = (query: string) => /(?:^|&)rec=([^&]*)/.exec(query)?.[1];

export function parseRoute(hash: string): Route {
  const [path, query = ""] = hash.replace(/^#/, "").split("?");
  const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  const q = new URLSearchParams(query);
  if (parts[0] === "ns" && parts[1]) {
    if (parts[2] === "x" && parts[3]) {
      const rec = decodeRec(rawRec(query));
      return rec ? { page: "execution", ns: parts[1], id: parts[3], rec } : { page: "execution", ns: parts[1], id: parts[3] };
    }
    return { page: "namespace", ns: parts[1], flow: q.get("flow") ?? undefined, status: q.get("status") ?? undefined };
  }
  return { page: "overview" };
}

export const href = {
  overview: () => "#/",
  namespace: (ns: string, q: { flow?: string; status?: string } = {}) => {
    const p = new URLSearchParams();
    if (q.flow) p.set("flow", q.flow);
    if (q.status) p.set("status", q.status);
    const s = p.toString();
    return `#/ns/${encodeURIComponent(ns)}${s ? "?" + s : ""}`;
  },
  execution: (ns: string, id: string, rec?: Attempt) =>
    `#/ns/${encodeURIComponent(ns)}/x/${encodeURIComponent(id)}${rec ? "?rec=" + encodeRec(rec) : ""}`,
};

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  const path = useRef(location.hash.split("?")[0]);
  useEffect(() => {
    const on = () => {
      setRoute(parseRoute(location.hash));
      // A new page starts at its top; a new ?rec= on the same page scrolls
      // to its record instead.
      const p = location.hash.split("?")[0];
      if (p !== path.current) window.scrollTo({ top: 0 });
      path.current = p;
    };
    addEventListener("hashchange", on);
    return () => removeEventListener("hashchange", on);
  }, []);
  return route;
}

/** Flow graphs never change for a version: they are fetched once. */
const graphs = new Map<string, Promise<api.Graph>>();

export function useGraph(ns: string, flow: string | undefined): api.Graph | undefined {
  const [g, setG] = useState<api.Graph>();
  useEffect(() => {
    if (!flow) return;
    const key = ns + "/" + flow;
    let p = graphs.get(key);
    if (!p) {
      p = api.flowGraph(ns, flow);
      p.catch(() => graphs.delete(key));
      graphs.set(key, p);
    }
    let live = true;
    p.then((v) => live && setG(v)).catch(() => undefined);
    return () => {
      live = false;
    };
  }, [ns, flow]);
  return g;
}

/** Live endings across every namespace; calls on for each. */
export function useLive(on: (e: { namespace: string; exec_id: string; status: string }) => void) {
  const [connected, setConnected] = useState(false);
  const onRef = useRef(on);
  onRef.current = on;
  useEffect(() => {
    const es = new EventSource("/api/live");
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    es.addEventListener("ended", (e) => onRef.current(JSON.parse((e as MessageEvent).data)));
    return () => es.close();
  }, []);
  return connected;
}

/** Re-renders every ms, for "4m ago" and running durations. */
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}
