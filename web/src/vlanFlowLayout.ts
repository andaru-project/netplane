// SPDX-License-Identifier: AGPL-3.0-or-later
import type {
  Device,
  HopState,
  TopologyGraph,
  VLANFlow,
  VLANFlowAccessPort,
  VLANFlowFinding,
  VLANFlowGateway,
  VLANFlowSSID,
  VLANFlowVLAN,
  VLANMode,
} from './api';
import { findingKey } from './vlanUtils';

type FlowColumnKind = 'segments' | 'vlans' | 'devices' | 'endpoints';

interface FlowColumn {
  index: number;
  kind: FlowColumnKind;
  hopDistance?: number;
  label: string;
  x: number;
  width: number;
}

interface FlowSegmentCard {
  id: string;
  kind: 'gateway' | 'foreign_leak';
  vlanId: number;
  vlanName?: string;
  gateway?: VLANFlowGateway;
  finding?: VLANFlowFinding;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface FlowPortChip {
  id: string;
  side: 'left' | 'right';
  label: string;
  mode: VLANMode;
  state: HopState;
  hopEdgeId: string;
  x: number;
  y: number;
}

interface FlowDeviceCard {
  id: string;
  deviceId: string;
  hopDistance: number;
  columnIndex: number;
  reached: boolean;
  hasGateway: boolean;
  x: number;
  y: number;
  width: number;
  height: number;
  leftPorts: FlowPortChip[];
  rightPorts: FlowPortChip[];
  localAccessCount: number;
  localSsidCount: number;
}

interface FlowEndpointLine {
  text: string;
  tone: 'normal' | 'dim' | 'warn';
}

interface FlowEndpointCard {
  id: string;
  deviceId: string;
  vlanId: number;
  isForeignLeak: boolean;
  isWirelessOnly: boolean;
  lines: FlowEndpointLine[];
  allUnreachable: boolean;
  finding?: VLANFlowFinding;
  x: number;
  y: number;
  width: number;
  height: number;
}

type RibbonState = HopState | 'stranded';

export interface FlowRibbon {
  id: string;
  kind: 'segment_link' | 'hop' | 'endpoint_link';
  edgeId?: string;
  finding?: VLANFlowFinding;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  sameColumn: boolean;
  linkKind: string;
  fromMode: VLANMode;
  toMode: VLANMode;
  state: RibbonState;
  isForeignLeak?: boolean;
  foreignVlanId?: number;
}

interface SingleVlanFlowLayout {
  width: number;
  height: number;
  columns: FlowColumn[];
  segmentCards: FlowSegmentCard[];
  deviceCards: FlowDeviceCard[];
  endpointCards: FlowEndpointCard[];
  ribbons: FlowRibbon[];
}

interface AllVlansVlanCard {
  id: string;
  vlan: VLANFlowVLAN;
  findings: VLANFlowFinding[];
  x: number;
  y: number;
  width: number;
  height: number;
}

interface AllVlansDeviceCard {
  id: string;
  deviceId: string;
  hopDistance: number;
  columnIndex: number;
  vlanIds: number[];
  findings: VLANFlowFinding[];
  x: number;
  y: number;
  width: number;
  height: number;
}

interface AllVlansEndpointEntry {
  vlanId: number;
  accessCount: number;
  unreachableAccessCount: number;
  ssids: { name: string; reachable: boolean }[];
  hasLeak: boolean;
}

interface AllVlansEndpointCard {
  id: string;
  deviceId: string;
  entries: AllVlansEndpointEntry[];
  hasUnreachable: boolean;
  hasLeak: boolean;
  x: number;
  y: number;
  width: number;
  height: number;
}

interface AllVlansEdgeBundle {
  edgeId: string;
  fromDeviceId: string;
  linkKind: string;
  sameColumn: boolean;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  lanes: {
    vlanId: number;
    state: HopState;
    fromMode: VLANMode;
    toMode: VLANMode;
    finding?: VLANFlowFinding;
  }[];
  findings: VLANFlowFinding[];
}

interface AllVlansSegmentLink {
  vlanId: number;
  deviceId: string;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
}

interface AllVlansEndpointLink {
  deviceId: string;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  hasLeak: boolean;
  hasUnreachable: boolean;
}

interface AllVlansFlowLayout {
  width: number;
  height: number;
  columns: FlowColumn[];
  vlanCards: AllVlansVlanCard[];
  deviceCards: AllVlansDeviceCard[];
  endpointCards: AllVlansEndpointCard[];
  segmentLinks: AllVlansSegmentLink[];
  edgeBundles: AllVlansEdgeBundle[];
  endpointLinks: AllVlansEndpointLink[];
}

const PAD_X = 20;
const TOP_HEADER_Y = 48;
const PAD_BOTTOM = 28;
const COL_GAP = 148;
const CARD_GAP_Y = 22;

const SEGMENT_COL_W = 204;
const DEVICE_COL_W = 228;
const ENDPOINT_COL_W = 228;

function addAdj(adj: Map<string, Set<string>>, a: string, b: string) {
  if (a === b) return;
  let sa = adj.get(a);
  if (!sa) {
    sa = new Set();
    adj.set(a, sa);
  }
  sa.add(b);
  let sb = adj.get(b);
  if (!sb) {
    sb = new Set();
    adj.set(b, sb);
  }
  sb.add(a);
}

function displayName(devicesById: Map<string, Device> | undefined, id: string): string {
  return devicesById?.get(id)?.name || id;
}

/**
 * Computes deterministic BFS hop distances for `participating` devices starting from `seeds`.
 * Traverses `primaryAdj` (VLAN hops) first, then `fallbackAdj` (physical topology edges)
 * for any participating device stranded behind a missing hop, and places truly isolated
 * devices one column past the furthest connected tier.
 */
function computeHopDistances(
  participating: string[],
  seeds: string[],
  primaryAdj: Map<string, Set<string>>,
  fallbackAdj: Map<string, Set<string>>,
  devicesById?: Map<string, Device>,
): Map<string, number> {
  const partSet = new Set(participating);
  const dist = new Map<string, number>();
  if (participating.length === 0) return dist;

  const validSeeds = seeds.filter((s) => partSet.has(s));
  let startNodes = Array.from(new Set(validSeeds));

  if (startNodes.length === 0) {
    // Pick highest-degree device in primaryAdj, tie-broken by display name.
    const sorted = [...partSet].sort((a, b) => {
      const degDiff = (primaryAdj.get(b)?.size ?? 0) - (primaryAdj.get(a)?.size ?? 0);
      if (degDiff !== 0) return degDiff;
      return displayName(devicesById, a).localeCompare(displayName(devicesById, b));
    });
    startNodes = [sorted[0]];
  }

  const runBfs = (adj: Map<string, Set<string>>, initialQueue: string[]) => {
    const queue = [...initialQueue];
    while (queue.length > 0) {
      const cur = queue.shift()!;
      const d = dist.get(cur)!;
      const neighbors = Array.from(adj.get(cur) ?? []).sort((a, b) =>
        displayName(devicesById, a).localeCompare(displayName(devicesById, b)),
      );
      for (const nxt of neighbors) {
        if (!partSet.has(nxt) || dist.has(nxt)) continue;
        dist.set(nxt, d + 1);
        queue.push(nxt);
      }
    }
  };

  for (const s of startNodes) {
    dist.set(s, 0);
  }
  runBfs(primaryAdj, startNodes);

  // Secondary BFS over full topology edges for participating devices not reached via VLAN hops.
  if (dist.size < partSet.size && fallbackAdj.size > 0) {
    const assigned = Array.from(dist.keys()).sort((a, b) => dist.get(a)! - dist.get(b)!);
    runBfs(fallbackAdj, assigned);
  }

  // Remaining disconnected devices go into maxDist + 1 (or 0 if none).
  if (dist.size < partSet.size) {
    let maxD = -1;
    for (const d of dist.values()) {
      if (d > maxD) maxD = d;
    }
    const orphanDist = maxD >= 0 ? maxD + 1 : 0;
    for (const id of [...partSet].sort((a, b) => displayName(devicesById, a).localeCompare(displayName(devicesById, b)))) {
      if (!dist.has(id)) dist.set(id, orphanDist);
    }
  }

  return dist;
}

function buildTopoAdj(topology?: TopologyGraph | null): Map<string, Set<string>> {
  const adj = new Map<string, Set<string>>();
  if (!topology) return adj;
  const deviceIds = new Set(topology.nodes.filter((n) => n.kind === 'device').map((n) => n.id));
  for (const e of topology.edges) {
    if (deviceIds.has(e.from_id) && deviceIds.has(e.to_id)) {
      addAdj(adj, e.from_id, e.to_id);
    }
  }
  return adj;
}

function deviceColumnLabel(colNum: number, dist: number, hasGateways: boolean, maxDist: number): string {
  if (hasGateways && dist === 0) return `${colNum}. GATEWAYS`;
  if (dist === 0) return `${colNum}. CORE FABRIC`;
  if (dist === 1 && maxDist >= 2) return `${colNum}. CORE FABRIC`;
  if (dist === 1) return `${colNum}. SWITCHES & APs`;
  if (dist === 2) return `${colNum}. DISTRIBUTION`;
  return `${colNum}. HOP ${dist}`;
}

function orderByBarycenter(
  deviceIds: string[],
  prevOrder: Map<string, number>,
  adj: Map<string, Set<string>>,
  devicesById?: Map<string, Device>,
): string[] {
  return [...deviceIds].sort((a, b) => {
    const score = (id: string) => {
      const parents = Array.from(adj.get(id) ?? []).filter((p) => prevOrder.has(p));
      if (parents.length === 0) return Number.MAX_SAFE_INTEGER;
      return parents.reduce((sum, p) => sum + prevOrder.get(p)!, 0) / parents.length;
    };
    const diff = score(a) - score(b);
    if (diff !== 0) return diff;
    return displayName(devicesById, a).localeCompare(displayName(devicesById, b));
  });
}

function colHeight(items: { height: number }[]): number {
  if (items.length === 0) return 0;
  return items.reduce((s, it) => s + it.height, 0) + CARD_GAP_Y * (items.length - 1);
}

function stackColumn<T extends { height: number; y: number }>(items: T[], totalHeight: number) {
  if (items.length === 0) return;
  const contentH = colHeight(items);
  let y = TOP_HEADER_Y + Math.max(0, (totalHeight - TOP_HEADER_Y - PAD_BOTTOM - contentH) / 2);
  for (const it of items) {
    it.y = Math.round(y);
    y += it.height + CARD_GAP_Y;
  }
}

function railY(top: number, span: number, idx: number, count: number): number {
  return count === 1 ? Math.round(top + span / 2) : Math.round(top + (span * idx) / (count - 1));
}

function orientEdge<T extends { columnIndex: number; x: number; y: number; width: number }>(cFrom: T, cTo: T) {
  const sameCol = cFrom.columnIndex === cTo.columnIndex;
  const swap = sameCol ? cFrom.y > cTo.y : cFrom.columnIndex > cTo.columnIndex;
  const leftCard = swap ? cTo : cFrom;
  const rightCard = swap ? cFrom : cTo;
  return {
    sameCol,
    swap,
    leftCard,
    rightCard,
    x1: leftCard.x + leftCard.width,
    x2: sameCol ? rightCard.x + rightCard.width : rightCard.x,
  };
}

interface LayeredGrid {
  byDist: Map<number, string[]>;
  distLevels: number[];
  columns: FlowColumn[];
  distToColIdx: Map<number, number>;
  devColMap: Map<string, number>;
  endpointColIdx: number;
  width: number;
}

/**
 * Shared layering & column-grid core for both Single-VLAN and All-VLANs layouts:
 * seeds & hop distances, barycenter tier ordering, and left-to-right column placement.
 */
function buildFlowGrid(opts: {
  participating: string[];
  gatewayDeviceIds: string[];
  hopAdj: Map<string, Set<string>>;
  topology?: TopologyGraph | null;
  devicesById?: Map<string, Device>;
  leadColumn: { kind: 'segments' | 'vlans'; label: string } | null;
  hasEndpointCol: boolean;
  hasGateways: boolean;
  firstTierCompare: (a: string, b: string) => number;
  minEmptyColW: number;
}): LayeredGrid {
  const partSet = new Set(opts.participating);
  const topoAdj = buildTopoAdj(opts.topology);
  const seeds = opts.gatewayDeviceIds.length > 0
    ? opts.gatewayDeviceIds
    : (opts.topology?.root_ids ?? []).filter((id) => partSet.has(id));

  const hopDistances = computeHopDistances(opts.participating, seeds, opts.hopAdj, topoAdj, opts.devicesById);

  const byDist = new Map<number, string[]>();
  for (const devId of opts.participating) {
    const d = hopDistances.get(devId) ?? 0;
    byDist.set(d, [...(byDist.get(d) ?? []), devId]);
  }
  const distLevels = Array.from(byDist.keys()).sort((a, b) => a - b);
  const maxDist = distLevels.length > 0 ? distLevels[distLevels.length - 1] : 0;

  const prevOrder = new Map<string, number>();
  const orderAdj = opts.hopAdj.size > 0 ? opts.hopAdj : topoAdj;
  for (const d of distLevels) {
    const ids = byDist.get(d)!;
    const ordered = d === distLevels[0]
      ? [...ids].sort(opts.firstTierCompare)
      : orderByBarycenter(ids, prevOrder, orderAdj, opts.devicesById);
    byDist.set(d, ordered);
    ordered.forEach((id, idx) => prevOrder.set(id, idx));
  }

  const columns: FlowColumn[] = [];
  let cursorX = PAD_X;
  const distToColIdx = new Map<number, number>();

  if (opts.leadColumn) {
    columns.push({
      index: columns.length,
      kind: opts.leadColumn.kind,
      label: opts.leadColumn.label,
      x: cursorX,
      width: SEGMENT_COL_W,
    });
    cursorX += SEGMENT_COL_W + COL_GAP;
  }

  for (const d of distLevels) {
    const colIdx = columns.length;
    distToColIdx.set(d, colIdx);
    columns.push({
      index: colIdx,
      kind: 'devices',
      hopDistance: d,
      label: deviceColumnLabel(colIdx + 1, d, opts.hasGateways, maxDist),
      x: cursorX,
      width: DEVICE_COL_W,
    });
    cursorX += DEVICE_COL_W + COL_GAP;
  }

  const endpointColIdx = opts.hasEndpointCol ? columns.length : -1;
  if (opts.hasEndpointCol) {
    columns.push({
      index: endpointColIdx,
      kind: 'endpoints',
      label: `${endpointColIdx + 1}. ENDPOINTS`,
      x: cursorX,
      width: ENDPOINT_COL_W,
    });
    cursorX += ENDPOINT_COL_W + PAD_X;
  } else {
    cursorX = Math.max(cursorX - COL_GAP + PAD_X, PAD_X * 2 + opts.minEmptyColW);
  }

  const devColMap = new Map<string, number>();
  for (const d of distLevels) {
    const cIdx = distToColIdx.get(d)!;
    for (const devId of byDist.get(d) ?? []) {
      devColMap.set(devId, cIdx);
    }
  }

  return {
    byDist,
    distLevels,
    columns,
    distToColIdx,
    devColMap,
    endpointColIdx,
    width: Math.max(720, Math.round(cursorX)),
  };
}

function buildDeviceColumnCards<T>(
  grid: LayeredGrid,
  makeCard: (devId: string, d: number, colIdx: number, col: FlowColumn) => T,
): { byId: Map<string, T>; byCol: Map<number, T[]> } {
  const byId = new Map<string, T>();
  const byCol = new Map<number, T[]>();
  for (const d of grid.distLevels) {
    const colIdx = grid.distToColIdx.get(d)!;
    const col = grid.columns[colIdx];
    const cards: T[] = [];
    for (const devId of grid.byDist.get(d) ?? []) {
      const card = makeCard(devId, d, colIdx, col);
      byId.set(devId, card);
      cards.push(card);
    }
    byCol.set(colIdx, cards);
  }
  return { byId, byCol };
}

function stackFlowColumns<
  TLead extends { height: number; y: number },
  TDev extends { height: number; y: number },
  TEp extends { height: number; y: number },
>(
  leadCards: TLead[],
  deviceCardsByCol: Map<number, TDev[]>,
  endpointCards: TEp[],
  minTotalH: number,
  minContentH: number,
  sortEndpoints: (a: TEp, b: TEp) => number,
): number {
  const heights = [
    colHeight(leadCards),
    ...Array.from(deviceCardsByCol.values()).map((cards) => colHeight(cards)),
    colHeight(endpointCards),
  ];
  const totalHeight = Math.max(minTotalH, TOP_HEADER_Y + Math.max(...heights, minContentH) + PAD_BOTTOM);

  stackColumn(leadCards, totalHeight);
  for (const cards of deviceCardsByCol.values()) {
    stackColumn(cards, totalHeight);
  }
  endpointCards.sort(sortEndpoints);
  stackColumn(endpointCards, totalHeight);
  return Math.round(totalHeight);
}

/**
 * Computes a complete left-to-right layered column layout for a single VLAN from `/api/v1/vlans/flow`.
 * Columns: [0: L3 Zones & Segments] -> [1..K: Devices by hop distance from gateway devices] -> [K+1: Endpoints].
 */
export function computeSingleVlanLayout(
  vlan: VLANFlowVLAN,
  flow: VLANFlow,
  devicesById?: Map<string, Device>,
  topology?: TopologyGraph | null,
): SingleVlanFlowLayout {
  const vlansById = new Map(flow.vlans.map((v) => [v.id, v]));
  const vlanFindings = flow.findings.filter((f) => f.vlans.includes(vlan.id));
  const findingByEdge = new Map<string, VLANFlowFinding>();
  for (const f of vlanFindings) {
    const existing = findingByEdge.get(f.edge_id);
    if (!existing || (f.severity === 'crit' && existing.severity !== 'crit')) {
      findingByEdge.set(f.edge_id, f);
    }
  }

  // Collect participating devices and hop adjacency.
  const participatingSet = new Set<string>(vlan.devices);
  for (const g of vlan.gateways) participatingSet.add(g.device_id);
  const hopAdj = new Map<string, Set<string>>();
  for (const h of vlan.hops) {
    participatingSet.add(h.from_id);
    participatingSet.add(h.to_id);
    addAdj(hopAdj, h.from_id, h.to_id);
  }
  for (const ap of vlan.access_ports) participatingSet.add(ap.device_id);
  for (const s of vlan.ssids) participatingSet.add(s.device_id);

  const gatewayDeviceIds = Array.from(new Set(vlan.gateways.map((g) => g.device_id)));

  // Identify leak findings involving foreign VLANs.
  const leakFindings = vlanFindings.filter((f) => f.kind === 'leak');
  const foreignLeaks: { foreignVid: number; finding: VLANFlowFinding }[] = [];
  const seenForeign = new Set<number>();
  for (const lf of leakFindings) {
    const other = lf.vlans.find((v) => v !== vlan.id);
    if (other != null && !seenForeign.has(other)) {
      seenForeign.add(other);
      foreignLeaks.push({ foreignVid: other, finding: lf });
    }
  }

  // Build endpoint groups per device (access ports + SSIDs, plus foreign leaked endpoints).
  const accessByDev = new Map<string, VLANFlowAccessPort[]>();
  for (const ap of vlan.access_ports) {
    accessByDev.set(ap.device_id, [...(accessByDev.get(ap.device_id) ?? []), ap]);
  }
  const ssidsByDev = new Map<string, VLANFlowSSID[]>();
  for (const s of vlan.ssids) {
    ssidsByDev.set(s.device_id, [...(ssidsByDev.get(s.device_id) ?? []), s]);
  }

  type EndpointSpec = {
    id: string;
    deviceId: string;
    vlanId: number;
    isForeignLeak: boolean;
    accessPorts: VLANFlowAccessPort[];
    ssids: VLANFlowSSID[];
    finding?: VLANFlowFinding;
  };
  const foreignEndpointSpecs: EndpointSpec[] = [];
  for (const lf of leakFindings) {
    const otherVid = lf.vlans.find((v) => v !== vlan.id);
    if (otherVid == null) continue;
    const foreignVlan = vlansById.get(otherVid);
    if (!foreignVlan) continue;
    const targetDev = lf.vlans[0] === vlan.id ? lf.peer_id : lf.device_id;
    const fAps = foreignVlan.access_ports.filter((ap) => ap.device_id === targetDev);
    const fSsids = foreignVlan.ssids.filter((s) => s.device_id === targetDev);
    if (fAps.length > 0 || fSsids.length > 0) {
      foreignEndpointSpecs.push({
        id: `ep-leak:${targetDev}:v${otherVid}:${findingKey(lf)}`,
        deviceId: targetDev,
        vlanId: otherVid,
        isForeignLeak: true,
        accessPorts: fAps,
        ssids: fSsids,
        finding: lf,
      });
    }
  }

  const hasSegmentCol = vlan.gateways.length > 0 || foreignLeaks.length > 0;
  const hasEndpointCol =
    vlan.access_ports.length > 0 || vlan.ssids.length > 0 || foreignEndpointSpecs.length > 0;

  const grid = buildFlowGrid({
    participating: Array.from(participatingSet),
    gatewayDeviceIds,
    hopAdj,
    topology,
    devicesById,
    leadColumn: hasSegmentCol ? { kind: 'segments', label: '1. L3 ZONES & SEGMENTS' } : null,
    hasEndpointCol,
    hasGateways: vlan.gateways.length > 0,
    firstTierCompare: (a, b) => {
      const ga = gatewayDeviceIds.indexOf(a);
      const gb = gatewayDeviceIds.indexOf(b);
      if (ga !== -1 || gb !== -1) {
        return (ga === -1 ? 999 : ga) - (gb === -1 ? 999 : gb);
      }
      return displayName(devicesById, a).localeCompare(displayName(devicesById, b));
    },
    minEmptyColW: DEVICE_COL_W,
  });

  const endpointSpecs: EndpointSpec[] = [];
  for (const d of grid.distLevels) {
    for (const devId of grid.byDist.get(d) ?? []) {
      const aps = accessByDev.get(devId) ?? [];
      const ssids = ssidsByDev.get(devId) ?? [];
      if (aps.length > 0 || ssids.length > 0) {
        endpointSpecs.push({
          id: `ep:${devId}:v${vlan.id}`,
          deviceId: devId,
          vlanId: vlan.id,
          isForeignLeak: false,
          accessPorts: aps,
          ssids,
        });
      }
    }
  }
  endpointSpecs.push(...foreignEndpointSpecs);

  // Pre-count ports per device side to size device cards accurately.
  const deviceColOffset = hasSegmentCol ? 1 : 0;
  const leftPortCount = new Map<string, number>();
  const rightPortCount = new Map<string, number>();
  const inc = (m: Map<string, number>, k: string) => m.set(k, (m.get(k) ?? 0) + 1);

  for (const g of vlan.gateways) inc(leftPortCount, g.device_id);
  for (const h of vlan.hops) {
    const cFrom = grid.devColMap.get(h.from_id) ?? deviceColOffset;
    const cTo = grid.devColMap.get(h.to_id) ?? deviceColOffset;
    if (cFrom === cTo) {
      inc(rightPortCount, h.from_id);
      inc(rightPortCount, h.to_id);
    } else if (cFrom < cTo) {
      inc(rightPortCount, h.from_id);
      inc(leftPortCount, h.to_id);
    } else {
      inc(rightPortCount, h.to_id);
      inc(leftPortCount, h.from_id);
    }
  }
  for (const ep of endpointSpecs) inc(rightPortCount, ep.deviceId);

  // Create segment cards.
  const segmentCards: FlowSegmentCard[] = [];
  if (hasSegmentCol) {
    const segCol = grid.columns[0];
    vlan.gateways.forEach((gw, idx) => {
      const addrLines = Math.min(2, Math.max(1, gw.addresses.length));
      const h = 92 + addrLines * 16 + (gw.is_management ? 24 : 0);
      segmentCards.push({
        id: `gw:${gw.device_id}:${gw.segment_id}:${idx}`,
        kind: 'gateway',
        vlanId: vlan.id,
        vlanName: vlan.name,
        gateway: gw,
        x: segCol.x,
        y: 0,
        width: segCol.width,
        height: h,
      });
    });
    for (const fl of foreignLeaks) {
      const fv = vlansById.get(fl.foreignVid);
      segmentCards.push({
        id: `foreign:${fl.foreignVid}:${findingKey(fl.finding)}`,
        kind: 'foreign_leak',
        vlanId: fl.foreignVid,
        vlanName: fv?.name,
        gateway: fv?.gateways[0],
        finding: fl.finding,
        x: segCol.x,
        y: 0,
        width: segCol.width,
        height: 112,
      });
    }
  }

  // Create device cards.
  const reachedSet = new Set(vlan.reached);
  const hasGateway = vlan.gateways.length > 0;
  const { byId: deviceCardsById, byCol: deviceCardsByCol } = buildDeviceColumnCards<FlowDeviceCard>(
    grid,
    (devId, d, colIdx, col) => {
      const maxPorts = Math.max(leftPortCount.get(devId) ?? 0, rightPortCount.get(devId) ?? 0, 1);
      return {
        id: `dev:${devId}`,
        deviceId: devId,
        hopDistance: d,
        columnIndex: colIdx,
        reached: hasGateway ? reachedSet.has(devId) : true,
        hasGateway,
        x: col.x,
        y: 0,
        width: col.width,
        height: Math.max(114, 52 + maxPorts * 29 + 30),
        leftPorts: [],
        rightPorts: [],
        localAccessCount: (accessByDev.get(devId) ?? []).length,
        localSsidCount: (ssidsByDev.get(devId) ?? []).length,
      };
    },
  );

  // Create endpoint cards with pre-formatted text lines (first 4 ports / 3 SSIDs + overflow).
  const endpointCards: FlowEndpointCard[] = endpointSpecs.map((spec) => {
    const allUnreachable =
      !spec.isForeignLeak &&
      hasGateway &&
      spec.accessPorts.every((ap) => !ap.reachable) &&
      spec.ssids.every((s) => !s.reachable);
    const lines: FlowEndpointLine[] = [];
    for (const ap of spec.accessPorts.slice(0, 4)) {
      lines.push({
        text: `[U] ${ap.link}${ap.label ? ` · ${ap.label}` : ''}`,
        tone: allUnreachable ? 'dim' : 'normal',
      });
    }
    if (spec.accessPorts.length > 4) {
      lines.push({ text: `+${spec.accessPorts.length - 4} more access port(s)`, tone: 'dim' });
    }
    for (const s of spec.ssids.slice(0, 3)) {
      lines.push({
        text: `SSID ${s.name} (${s.client_count} cl)`,
        tone: !s.reachable && hasGateway ? 'warn' : 'normal',
      });
    }
    if (spec.ssids.length > 3) {
      lines.push({ text: `+${spec.ssids.length - 3} more SSID(s)`, tone: 'dim' });
    }
    const h = Math.max(76, 46 + lines.length * 18 + (spec.isForeignLeak ? 18 : 0));
    const col = grid.columns[grid.endpointColIdx];
    return {
      id: spec.id,
      deviceId: spec.deviceId,
      vlanId: spec.vlanId,
      isForeignLeak: spec.isForeignLeak,
      isWirelessOnly: spec.ssids.length > 0 && spec.accessPorts.length === 0,
      lines,
      allUnreachable,
      finding: spec.finding,
      x: col.x,
      y: 0,
      width: col.width,
      height: h,
    };
  });

  const height = stackFlowColumns(segmentCards, deviceCardsByCol, endpointCards, 320, 220, (a, b) => {
    const da = deviceCardsById.get(a.deviceId);
    const db = deviceCardsById.get(b.deviceId);
    const ya = da ? da.y + da.height / 2 : 0;
    const yb = db ? db.y + db.height / 2 : 0;
    if (ya !== yb) return ya - yb;
    return Number(a.isForeignLeak) - Number(b.isForeignLeak);
  });

  // Attach sorted port slots and build ribbons.
  type PortSlot = {
    side: 'left' | 'right';
    peerY: number;
    assignY: (y: number) => void;
    chip?: {
      label: string;
      mode: VLANMode;
      state: HopState;
      hopEdgeId: string;
    };
  };

  const slotsByDevice = new Map<string, PortSlot[]>();
  const addSlot = (devId: string, slot: PortSlot) => {
    slotsByDevice.set(devId, [...(slotsByDevice.get(devId) ?? []), slot]);
  };

  const ribbons: FlowRibbon[] = [];

  // 1) Gateway segment -> gateway device links.
  for (const sc of segmentCards) {
    if (sc.kind !== 'gateway' || !sc.gateway) continue;
    const devCard = deviceCardsById.get(sc.gateway.device_id);
    if (!devCard) continue;
    const ribbon: FlowRibbon = {
      id: `seg-link:${sc.id}`,
      kind: 'segment_link',
      x1: sc.x + sc.width,
      y1: sc.y + sc.height / 2,
      x2: devCard.x,
      y2: devCard.y + devCard.height / 2,
      sameColumn: false,
      linkKind: 'wired',
      fromMode: 'tagged',
      toMode: 'tagged',
      state: 'carried',
    };
    ribbons.push(ribbon);
    addSlot(devCard.deviceId, {
      side: 'left',
      peerY: ribbon.y1,
      assignY: (y) => {
        ribbon.y2 = y;
      },
    });
  }

  // 2) Infrastructure hop ribbons between devices.
  for (const h of vlan.hops) {
    const cFrom = deviceCardsById.get(h.from_id);
    const cTo = deviceCardsById.get(h.to_id);
    if (!cFrom || !cTo) continue;

    const finding = findingByEdge.get(h.edge_id);
    const { sameCol, swap, leftCard, rightCard, x1, x2 } = orientEdge(cFrom, cTo);
    const leftLink = (swap ? h.to_link : h.from_link) || (h.kind === 'wireless_uplink' ? 'mesh' : 'link');
    const rightLink = (swap ? h.from_link : h.to_link) || (h.kind === 'wireless_uplink' ? 'mesh' : 'link');
    const leftMode: VLANMode = swap ? h.to_mode : h.from_mode;
    const rightMode: VLANMode = swap ? h.from_mode : h.to_mode;
    const foreignVid = finding?.kind === 'leak' ? finding.vlans.find((v) => v !== vlan.id) : undefined;

    const ribbon: FlowRibbon = {
      id: `hop:${h.edge_id}`,
      kind: 'hop',
      edgeId: h.edge_id,
      finding,
      x1,
      y1: leftCard.y + leftCard.height / 2,
      x2,
      y2: rightCard.y + rightCard.height / 2,
      sameColumn: sameCol,
      linkKind: h.kind,
      fromMode: leftMode,
      toMode: rightMode,
      state: h.state,
      foreignVlanId: foreignVid,
    };
    ribbons.push(ribbon);

    addSlot(leftCard.deviceId, {
      side: 'right',
      peerY: rightCard.y + rightCard.height / 2,
      assignY: (y) => {
        ribbon.y1 = y;
      },
      chip: { label: leftLink, mode: leftMode, state: h.state, hopEdgeId: h.edge_id },
    });
    addSlot(rightCard.deviceId, {
      side: sameCol ? 'right' : 'left',
      peerY: leftCard.y + leftCard.height / 2,
      assignY: (y) => {
        ribbon.y2 = y;
      },
      chip: { label: rightLink, mode: rightMode, state: h.state, hopEdgeId: h.edge_id },
    });
  }

  // 3) Device -> Endpoint ribbons.
  for (const ep of endpointCards) {
    const devCard = deviceCardsById.get(ep.deviceId);
    if (!devCard) continue;
    const ribbon: FlowRibbon = {
      id: `ep-link:${ep.id}`,
      kind: 'endpoint_link',
      finding: ep.finding,
      x1: devCard.x + devCard.width,
      y1: devCard.y + devCard.height / 2,
      x2: ep.x,
      y2: ep.y + ep.height / 2,
      sameColumn: false,
      linkKind: ep.isWirelessOnly ? 'wireless_uplink' : 'wired',
      fromMode: 'untagged',
      toMode: 'untagged',
      state: ep.isForeignLeak ? 'leak' : ep.allUnreachable ? 'stranded' : 'carried',
      isForeignLeak: ep.isForeignLeak,
      foreignVlanId: ep.isForeignLeak ? ep.vlanId : undefined,
    };
    ribbons.push(ribbon);
    addSlot(devCard.deviceId, {
      side: 'right',
      peerY: ep.y + ep.height / 2,
      assignY: (y) => {
        ribbon.y1 = y;
      },
    });
  }

  // Place port chips neatly along each device card's left and right rails.
  const deviceCards = Array.from(deviceCardsById.values());
  for (const card of deviceCards) {
    const slots = slotsByDevice.get(card.deviceId) ?? [];
    for (const side of ['left', 'right'] as const) {
      const sideSlots = slots.filter((s) => s.side === side).sort((a, b) => a.peerY - b.peerY);
      const railTop = card.y + 46;
      const span = Math.max(0, card.height - 72);
      sideSlots.forEach((slot, idx) => {
        const y = railY(railTop, span, idx, sideSlots.length);
        slot.assignY(y);
        if (slot.chip) {
          const chip: FlowPortChip = {
            id: `${card.deviceId}:${side}:${slot.chip.hopEdgeId}:${idx}`,
            side,
            label: slot.chip.label,
            mode: slot.chip.mode,
            state: slot.chip.state,
            hopEdgeId: slot.chip.hopEdgeId,
            x: side === 'left' ? card.x + 6 : card.x + card.width - 6,
            y,
          };
          if (side === 'left') card.leftPorts.push(chip);
          else card.rightPorts.push(chip);
        }
      });
    }
  }

  return {
    width: grid.width,
    height,
    columns: grid.columns,
    segmentCards,
    deviceCards,
    endpointCards,
    ribbons,
  };
}

/**
 * Computes a complete left-to-right column layout for the "All VLANs" overview mode.
 */
export function computeAllVlansLayout(
  flow: VLANFlow,
  devicesById?: Map<string, Device>,
  topology?: TopologyGraph | null,
): AllVlansFlowLayout {
  const vlans = [...flow.vlans].sort((a, b) => a.id - b.id);
  const participatingSet = new Set<string>();
  const gatewayDeviceSet = new Set<string>();
  const hopAdj = new Map<string, Set<string>>();
  const devVlans = new Map<string, Set<number>>();

  const trackDevVlan = (devId: string, vid: number) => {
    participatingSet.add(devId);
    let s = devVlans.get(devId);
    if (!s) {
      s = new Set();
      devVlans.set(devId, s);
    }
    s.add(vid);
  };

  for (const v of vlans) {
    for (const g of v.gateways) {
      gatewayDeviceSet.add(g.device_id);
      trackDevVlan(g.device_id, v.id);
    }
    for (const d of v.devices) trackDevVlan(d, v.id);
    for (const h of v.hops) {
      trackDevVlan(h.from_id, v.id);
      trackDevVlan(h.to_id, v.id);
      addAdj(hopAdj, h.from_id, h.to_id);
    }
    for (const ap of v.access_ports) trackDevVlan(ap.device_id, v.id);
    for (const s of v.ssids) trackDevVlan(s.device_id, v.id);
  }

  // Endpoint summaries per device.
  const endpointEntriesByDev = new Map<string, AllVlansEndpointEntry[]>();
  for (const v of vlans) {
    const vHasLeak = flow.findings.some((f) => f.kind === 'leak' && f.vlans.includes(v.id));
    const hasGateway = v.gateways.length > 0;
    const devsHere = new Set<string>([
      ...v.access_ports.map((ap) => ap.device_id),
      ...v.ssids.map((s) => s.device_id),
    ]);
    for (const devId of devsHere) {
      const aps = v.access_ports.filter((ap) => ap.device_id === devId);
      const ssids = v.ssids.filter((s) => s.device_id === devId);
      const devLeak =
        vHasLeak &&
        flow.findings.some(
          (f) => f.kind === 'leak' && f.vlans.includes(v.id) && (f.device_id === devId || f.peer_id === devId),
        );
      endpointEntriesByDev.set(devId, [
        ...(endpointEntriesByDev.get(devId) ?? []),
        {
          vlanId: v.id,
          accessCount: aps.length,
          unreachableAccessCount: hasGateway ? aps.filter((ap) => !ap.reachable).length : 0,
          ssids: ssids.map((s) => ({ name: s.name, reachable: !hasGateway || s.reachable })),
          hasLeak: devLeak,
        },
      ]);
    }
  }

  const hasEndpointCol = endpointEntriesByDev.size > 0;
  const grid = buildFlowGrid({
    participating: Array.from(participatingSet),
    gatewayDeviceIds: Array.from(gatewayDeviceSet),
    hopAdj,
    topology,
    devicesById,
    leadColumn: { kind: 'vlans', label: '1. ALL VLANS & SEGMENTS' },
    hasEndpointCol,
    hasGateways: gatewayDeviceSet.size > 0,
    firstTierCompare: (a, b) => displayName(devicesById, a).localeCompare(displayName(devicesById, b)),
    minEmptyColW: SEGMENT_COL_W,
  });

  // Create VLAN cards in Col 0.
  const vlanCards: AllVlansVlanCard[] = vlans.map((v) => ({
    id: `vlan:${v.id}`,
    vlan: v,
    findings: flow.findings.filter((f) => f.vlans.includes(v.id)),
    x: grid.columns[0].x,
    y: 0,
    width: grid.columns[0].width,
    height: 78,
  }));
  const vlanCardById = new Map(vlanCards.map((c) => [c.vlan.id, c]));

  // Create device cards.
  const { byId: deviceCardsById, byCol: deviceCardsByCol } = buildDeviceColumnCards<AllVlansDeviceCard>(
    grid,
    (devId, d, colIdx, col) => {
      const vIds = Array.from(devVlans.get(devId) ?? []).sort((a, b) => a - b);
      return {
        id: `dev:${devId}`,
        deviceId: devId,
        hopDistance: d,
        columnIndex: colIdx,
        vlanIds: vIds,
        findings: flow.findings.filter((f) => f.device_id === devId || f.peer_id === devId),
        x: col.x,
        y: 0,
        width: col.width,
        height: Math.max(106, 68 + Math.min(vIds.length, 6) * 14),
      };
    },
  );

  // Create endpoint summary cards.
  const endpointCards: AllVlansEndpointCard[] = [];
  if (hasEndpointCol) {
    const col = grid.columns[grid.endpointColIdx];
    for (const [devId, entries] of endpointEntriesByDev.entries()) {
      const visible = Math.min(entries.length, 5);
      endpointCards.push({
        id: `all-ep:${devId}`,
        deviceId: devId,
        entries,
        hasUnreachable: entries.some((e) => e.unreachableAccessCount > 0 || e.ssids.some((s) => !s.reachable)),
        hasLeak: entries.some((e) => e.hasLeak),
        x: col.x,
        y: 0,
        width: col.width,
        height: Math.max(82, 46 + visible * 18 + (entries.length > 5 ? 16 : 0)),
      });
    }
  }

  const height = stackFlowColumns(
    vlanCards,
    deviceCardsByCol,
    endpointCards,
    340,
    240,
    (a, b) => (deviceCardsById.get(a.deviceId)?.y ?? 0) - (deviceCardsById.get(b.deviceId)?.y ?? 0),
  );

  // Segment -> gateway device links (or first device for L2-only VLAN).
  const segmentLinks: AllVlansSegmentLink[] = [];
  for (const vc of vlanCards) {
    const targets = vc.vlan.gateways.length > 0
      ? Array.from(new Set(vc.vlan.gateways.map((g) => g.device_id)))
      : vc.vlan.devices.slice(0, 1);
    for (const devId of targets) {
      const dc = deviceCardsById.get(devId);
      if (!dc) continue;
      segmentLinks.push({
        vlanId: vc.vlan.id,
        deviceId: devId,
        x1: vc.x + vc.width,
        y1: vc.y + vc.height / 2,
        x2: dc.x,
        y2: dc.y + dc.height / 2,
      });
    }
  }

  // Spread segment links neatly along the receiving gateway device's left edge.
  const segLinksByDev = new Map<string, AllVlansSegmentLink[]>();
  for (const sl of segmentLinks) {
    segLinksByDev.set(sl.deviceId, [...(segLinksByDev.get(sl.deviceId) ?? []), sl]);
  }
  for (const [devId, links] of segLinksByDev.entries()) {
    const dc = deviceCardsById.get(devId);
    if (!dc) continue;
    links.sort((a, b) => (vlanCardById.get(a.vlanId)?.y ?? 0) - (vlanCardById.get(b.vlanId)?.y ?? 0));
    const top = dc.y + 42;
    const span = Math.max(0, dc.height - 58);
    links.forEach((l, i) => {
      l.y2 = railY(top, span, i, links.length);
    });
  }

  // Group hops by edge_id into multi-lane bundles.
  const bundleMap = new Map<string, AllVlansEdgeBundle>();
  for (const v of vlans) {
    for (const h of v.hops) {
      const cFrom = deviceCardsById.get(h.from_id);
      const cTo = deviceCardsById.get(h.to_id);
      if (!cFrom || !cTo) continue;

      const { sameCol, swap, leftCard, rightCard, x1, x2 } = orientEdge(cFrom, cTo);
      let bundle = bundleMap.get(h.edge_id);
      if (!bundle) {
        bundle = {
          edgeId: h.edge_id,
          fromDeviceId: leftCard.deviceId,
          linkKind: h.kind,
          sameColumn: sameCol,
          x1,
          y1: leftCard.y + leftCard.height / 2,
          x2,
          y2: rightCard.y + rightCard.height / 2,
          lanes: [],
          findings: flow.findings.filter((f) => f.edge_id === h.edge_id),
        };
        bundleMap.set(h.edge_id, bundle);
      }
      bundle.lanes.push({
        vlanId: v.id,
        state: h.state,
        fromMode: swap ? h.to_mode : h.from_mode,
        toMode: swap ? h.from_mode : h.to_mode,
        finding: bundle.findings.find((f) => f.vlans.includes(v.id)),
      });
    }
  }

  const edgeBundles = Array.from(bundleMap.values());
  // Spread outgoing edge bundles along each source device's right edge.
  const outByDev = new Map<string, AllVlansEdgeBundle[]>();
  for (const b of edgeBundles) {
    outByDev.set(b.fromDeviceId, [...(outByDev.get(b.fromDeviceId) ?? []), b]);
  }
  for (const [devId, bundles] of outByDev.entries()) {
    const dc = deviceCardsById.get(devId);
    if (!dc || bundles.length <= 1) continue;
    bundles.sort((a, b) => a.y2 - b.y2);
    const top = dc.y + 44;
    const span = Math.max(0, dc.height - 62);
    bundles.forEach((b, i) => {
      b.y1 = railY(top, span, i, bundles.length);
    });
  }

  // Device -> endpoint links.
  const endpointLinks: AllVlansEndpointLink[] = [];
  for (const ep of endpointCards) {
    const dc = deviceCardsById.get(ep.deviceId);
    if (!dc) continue;
    endpointLinks.push({
      deviceId: ep.deviceId,
      x1: dc.x + dc.width,
      y1: dc.y + dc.height / 2,
      x2: ep.x,
      y2: ep.y + ep.height / 2,
      hasLeak: ep.hasLeak,
      hasUnreachable: ep.hasUnreachable,
    });
  }

  return {
    width: grid.width,
    height,
    columns: grid.columns,
    vlanCards,
    deviceCards: Array.from(deviceCardsById.values()),
    endpointCards,
    segmentLinks,
    edgeBundles,
    endpointLinks,
  };
}
