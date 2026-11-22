// SPDX-License-Identifier: AGPL-3.0-or-later

export interface SensorReading {
  name: string;
  kind: string;
  value: number;
  unit: string;
  status: 'ok' | 'warn' | 'crit' | 'unknown';
}

export interface PoEBudget {
  total_watts: number;
  used_watts: number;
}

export interface Device {
  id: string;
  driver: string;
  target_id: string;
  name: string;
  hostname?: string;
  vendor?: string;
  model?: string;
  role: string;
  primary_mac?: string;
  macs: string[];
  mgmt_ip?: string;
  firmware: { current?: string; available?: string; serial?: string };
  uptime_sec: number;
  online: boolean;
  last_seen: string;
  capabilities: string[];
  system_stats: { cpu_pct?: number; mem_used_bytes?: number; mem_total_bytes?: number; load1?: number };
  sensors: SensorReading[];
  poe_budget?: PoEBudget | null;
}

export interface SfpDom {
  present: boolean;
  vendor?: string;
  part_number?: string;
  serial?: string;
  wavelength_nm?: number;
  temp_c?: number;
  voltage_v?: number;
  bias_ma?: number;
  tx_power_dbm?: number;
  rx_power_dbm?: number;
  alert_state: 'ok' | 'warn' | 'crit' | 'unknown';
}

export interface Link {
  device_id: string;
  index: number;
  if_name: string;
  label?: string;
  admin_up: boolean;
  link_up: boolean;
  oper_state: string;
  speed_mbps: number;
  duplex?: string;
  mtu?: number;
  media: string;
  is_fiber: boolean;
  lag_group_id?: string;
  stp_state?: string;
  eee?: boolean;
  flow_control?: { rx: boolean; tx: boolean; autoneg?: boolean };
  pvid?: number;
  tagged_vlans: number[];
  poe?: { supported: boolean; enabled: boolean; state?: string; watts?: number; class?: string };
  sfp?: SfpDom;
  counters?: { rx_bytes: number; tx_bytes: number; rx_errors: number; tx_errors: number; rx_dropped: number; tx_dropped: number };
  rates?: { rx_bps: number; tx_bps: number };
}

export interface Segment {
  id: string;
  name: string;
  kind: string;
  parent_link?: string;
  vlan_id?: number;
  oper_state: string;
  addresses: { family: string; address: string; prefix_length: number; scope?: string }[];
  ha_state?: { protocol: string; vhid: number; state: string; vip: string };
}

export interface GatewayStatus {
  name: string;
  gateway_ip: string;
  monitor_ip?: string;
  online: boolean;
  rtt_ms?: number;
  loss_pct?: number;
  is_default?: boolean;
}

export interface VpnTunnel {
  id: string;
  name: string;
  kind: string;
  endpoint?: string;
  up: boolean;
  last_handshake?: string;
  rx_bytes: number;
  tx_bytes: number;
}

export interface FirewallState {
  engine: string;
  states_used?: number;
  states_max?: number;
  policy_rule_count?: number;
  nat_rule_count?: number;
  saved_version?: string;
  services: { name: string; state: string; summary?: string }[];
}

export interface WirelessRadio {
  name: string;
  band: string;
  channel: number;
  width_mhz: number;
  tx_power_dbm: number;
  ssids: { name: string; enabled: boolean; client_count: number; security?: string }[];
}

export interface Client {
  mac: string;
  ips: string[];
  hostname?: string;
  vendor_oui?: string;
  online: boolean;
  connection_type: string;
  attached_device_id?: string;
  attached_link?: string;
  ssid?: string;
  vlan?: number;
  signal_dbm?: number;
  tx_rate_mbps?: number;
  rx_rate_mbps?: number;
  last_seen: string;
}

export interface DiagnosticResult {
  id: string;
  kind: string;
  device_id?: string;
  target: string;
  ok: boolean;
  started_at: string;
  duration_ms: number;
  summary: string;
  structured?: Record<string, unknown>;
  raw_output?: string;
  error?: string;
}

export interface DeviceDetail {
  device: Device;
  links: Link[];
  segments: Segment[];
  gateways: GatewayStatus[];
  vpns: VpnTunnel[];
  firewall?: FirewallState;
  radios: WirelessRadio[];
  clients: Client[];
  recent_diagnostics: DiagnosticResult[];
}

export interface TopologyNode {
  id: string;
  kind: 'device' | 'client';
  role?: string;
  driver?: string;
  label: string;
  mac?: string;
  ip?: string;
  online: boolean;
}

export interface TopologyEdge {
  id: string;
  from_id: string;
  from_link?: string;
  to_id: string;
  to_link?: string;
  kind: string;
  evidence: string;
  confidence: number;
  speed_mbps?: number;
  member_links: string[];
}

export interface TopologyGraph {
  generated_at: string;
  root_ids: string[];
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

export interface NetplaneEvent {
  id: string;
  type: string;
  severity: 'info' | 'warn' | 'crit';
  timestamp: string;
  device_id?: string;
  link_name?: string;
  summary: string;
}

// Fired after a token is stored, so the event stream can reconnect at once.
export const AUTH_EVENT = 'netplane-auth';

// One shared prompt so parallel 401s ask for the token only once.
let tokenPrompt: Promise<boolean> | null = null;
// Bumped on each stored token, so a 401 sent before it retries with the new cookie instead of prompting again.
let authGen = 0;

// promptForToken stores the API token in a SameSite=Strict cookie, which fetch and
// EventSource both send; EventSource cannot set an Authorization header.
function promptForToken(): Promise<boolean> {
  tokenPrompt ??= Promise.resolve().then(() => {
    const token = window.prompt('This netplane server requires an API token:');
    if (!token) return false;
    // Scope to the app's base path so other apps on the origin (e.g. other HA ingress add-ons) never get it.
    const path = new URL('.', document.baseURI).pathname;
    document.cookie = `netplane_token=${encodeURIComponent(token)}; path=${path}; SameSite=Strict`;
    authGen++;
    window.dispatchEvent(new Event(AUTH_EVENT));
    return true;
  }).finally(() => {
    tokenPrompt = null;
  });
  return tokenPrompt;
}

async function fetchJSON<T>(path: string, init?: RequestInit, retried = false): Promise<T> {
  const gen = authGen;
  const resp = await fetch(`./api/v1${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  });
  if (resp.status === 401 && !retried && (gen !== authGen || (await promptForToken()))) {
    return fetchJSON<T>(path, init, true);
  }
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`HTTP ${resp.status}: ${text}`);
  }
  return resp.json() as Promise<T>;
}

export const api = {
  listDevices: () => fetchJSON<{ items: Device[] }>('/devices').then((r) => r.items),
  getDevice: (id: string) => fetchJSON<DeviceDetail>(`/devices/${encodeURIComponent(id)}`),
  getTopology: () => fetchJSON<TopologyGraph>('/topology'),
  listClients: (q = '', onlineOnly = false) => {
    const params = new URLSearchParams();
    if (onlineOnly) params.set('online', 'true');
    if (q) params.set('q', q);
    const qs = params.toString();
    return fetchJSON<{ items: Client[] }>(`/clients${qs ? '?' + qs : ''}`).then((r) => r.items);
  },
  listDiagnostics: (limit = 30) =>
    fetchJSON<{ items: DiagnosticResult[] }>(`/diagnostics?limit=${limit}`).then((r) => r.items),
  runDiagnostic: (req: { kind: string; device_id?: string; target: string }) =>
    fetchJSON<DiagnosticResult>('/diagnostics', { method: 'POST', body: JSON.stringify(req) }),
};

export function formatSpeed(mbps: number): string {
  if (!mbps) return '—';
  if (mbps >= 1000) return `${mbps / 1000}G`;
  return `${mbps}M`;
}

export function formatBps(bps?: number): string {
  if (!bps || bps <= 0) return '0 bps';
  if (bps >= 1e9) return `${(bps / 1e9).toFixed(2)} Gbps`;
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} Mbps`;
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(0)} kbps`;
  return `${Math.round(bps)} bps`;
}

export function formatUptime(sec: number): string {
  if (!sec) return '—';
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return d > 0 ? `${d}d ${h}h` : `${h}h ${m}m`;
}
