// SPDX-License-Identifier: AGPL-3.0-or-later
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  api,
  apiUrl,
  AUTH_EVENT,
  type DiagnosticResult,
  type NetplaneEvent,
  type NetworkState,
  type TargetConfig,
  type TopologyGraph,
  type VLANFlow,
} from './api';
import { Shell } from './Shell';

const SSE_EVENT_TYPES = [
  'device.online',
  'device.offline',
  'link.up',
  'link.down',
  'link.stp_changed',
  'sfp.alert',
  'vpn.up',
  'vpn.down',
  'gateway.up',
  'gateway.down',
  'topology.updated',
  'client.joined',
  'client.left',
];

export function App() {
  const [sseEpoch, setSseEpoch] = useState(0);
  const sseFailures = useRef(0);

  const [state, setState] = useState<NetworkState | null>(null);
  const [topology, setTopology] = useState<TopologyGraph | null>(null);
  const [flow, setFlow] = useState<VLANFlow | null>(null);
  const [targets, setTargets] = useState<TargetConfig[]>([]);
  const [diagRuns, setDiagRuns] = useState<DiagnosticResult[]>([]);
  const [events, setEvents] = useState<NetplaneEvent[]>([]);
  const [sseConnected, setSseConnected] = useState(false);
  const [errMsg, setErrMsg] = useState<string | null>(null);
  const [pollBusy, setPollBusy] = useState(false);
  const [refreshNonce, setRefreshNonce] = useState(0);

  const refreshGen = useRef(0);

  const refreshAll = useCallback(async () => {
    const gen = ++refreshGen.current;
    try {
      const [st, topo, fl, tgts, diags] = await Promise.all([
        api.getState(),
        api.getTopology(),
        api.getVLANFlow(),
        api.listTargets(),
        api.listDiagnostics(30),
      ]);
      if (gen !== refreshGen.current) return;
      setState(st);
      setTopology(topo);
      setFlow(fl);
      setTargets(tgts);
      setDiagRuns(diags);
      setRefreshNonce((n) => n + 1);
      setErrMsg(null);
    } catch (e) {
      if (gen !== refreshGen.current) return;
      setErrMsg(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void refreshAll();
    const timer = window.setInterval(() => void refreshAll(), 15_000);
    return () => window.clearInterval(timer);
  }, [refreshAll]);

  const refreshRef = useRef(refreshAll);
  refreshRef.current = refreshAll;

  useEffect(() => {
    const onAuth = () => {
      sseFailures.current = 0;
      setSseEpoch((n) => n + 1);
      void refreshRef.current();
    };
    window.addEventListener(AUTH_EVENT, onAuth);
    return () => window.removeEventListener(AUTH_EVENT, onAuth);
  }, []);

  useEffect(() => {
    if (typeof EventSource === 'undefined') return;
    const es = new EventSource(apiUrl('/events/stream'));
    es.onopen = () => {
      sseFailures.current = 0;
      setSseConnected(true);
    };
    let retry: number | undefined;
    es.onerror = () => {
      setSseConnected(false);
      if (es.readyState === EventSource.CLOSED) {
        const delay = Math.min(60_000, 5_000 * 2 ** sseFailures.current++);
        retry = window.setTimeout(() => setSseEpoch((n) => n + 1), delay);
      }
    };
    const onEvent = (e: MessageEvent<string>) => {
      try {
        const parsed = JSON.parse(e.data) as NetplaneEvent;
        setEvents((prev) => [parsed, ...prev].slice(0, 50));
        if (parsed.type === 'topology.updated') {
          void refreshRef.current();
        }
      } catch {
        // Ignore non-JSON SSE keepalive/frames.
      }
    };
    for (const t of SSE_EVENT_TYPES) {
      es.addEventListener(t, onEvent as EventListener);
    }
    return () => {
      window.clearTimeout(retry);
      es.close();
    };
  }, [sseEpoch]);

  const handlePollAll = useCallback(async () => {
    setPollBusy(true);
    try {
      const enabled = targets.filter((t) => t.enabled);
      if (enabled.length > 0) {
        await Promise.allSettled(enabled.map((t) => api.pollTarget(t.id)));
      }
      await refreshAll();
    } catch (e) {
      setErrMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setPollBusy(false);
    }
  }, [targets, refreshAll]);

  const handleDiagnosticCompleted = useCallback((res: DiagnosticResult) => {
    refreshGen.current++;
    setDiagRuns((prev) => [res, ...prev]);
  }, []);

  return (
    <Shell
      state={state}
      topology={topology}
      flow={flow}
      targets={targets}
      diagRuns={diagRuns}
      events={events}
      sseConnected={sseConnected}
      errMsg={errMsg}
      pollBusy={pollBusy}
      refreshNonce={refreshNonce}
      onPollAll={() => void handlePollAll()}
      onDiagnosticCompleted={handleDiagnosticCompleted}
    />
  );
}
