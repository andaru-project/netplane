// SPDX-License-Identifier: AGPL-3.0-or-later
import type {
  Device,
  HopState,
  TopologyGraph,
  VLANFlow,
  VLANFlowAccessPort,
  VLANFlowFinding,
  VLANFlowGateway,
  VLANFlowHop,
  VLANFlowSSID,
  VLANFlowVLAN,
  VLANMode,
} from './api';
import { findingKey } from './vlanUtils';

export type FlowColumnKind = 'segments' | 'vlans' | 'devices' | 'endpoints';

export interface FlowColumn {
  index: number;
  kind: FlowColumnKind;
  hopDistance?: number;
  label: string;
  x: number;
  width: number;
  nodeIds: string[];
}

export interface FlowSegmentCard {
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
  isLeak: boolean;
  isDeadEndMissing: boolean;
  isMismatch: boolean;
}

export interface FlowDeviceCard {
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

export interface FlowEndpointCard {
  id: string;
  deviceId: string;
  vlanId: number;
  isForeignLeak: boolean;
  accessPorts: VLANFlowAccessPort[];
  ssids: VLANFlowSSID[];
  allUnreachable: boolean;
  finding?: VLANFlowFinding;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface FlowRibbon {
  id: string;
  kind: 'segment_link' | 'hop' | 'endpoint_link';
  edgeId?: string;
  hop?: VLANFlowHop;
  finding?: VLANFlowFinding;
  fromNodeId: string;
  toNodeId: string;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  sameColumn: boolean;
  linkKind: string;
  fromMode: VLANMode;
  toMode: VLANMode;
  state: HopState;
  isForeignLeak?: boolean;
  foreignVlanId?: number;
}

export interface SingleVlanFlowLayout {
  vlanId: number;
  width: number;
  height: number;
  columns: FlowColumn[];
  segmentCards: FlowSegmentCard[];
  deviceCards: FlowDeviceCard[];
  endpointCards: FlowEndpointCard[];
  ribbons: FlowRibbon[];
}

export interface AllVlansVlanCard {
  id: string;
  vlan: VLANFlowVLAN;
  findings: VLANFlowFinding[];
  hasUnverified: boolean;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface AllVlansDeviceCard {
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

export interface AllVlansEndpointEntry {
  vlanId: number;
  vlanName?: string;
  accessCount: number;
  unreachableAccessCount: number;
  ssids: { name: string; reachable: boolean }[];
  hasLeak: boolean;
}

export interface AllVlansEndpointCard {
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

export interface AllVlansEdgeBundle {
  edgeId: string;
  fromDeviceId: string;
  toDeviceId: string;
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

export interface AllVlansSegmentLink {
  vlanId: number;
  deviceId: string;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
}

export interface AllVlansEndpointLink {
  deviceId: string;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  hasLeak: boolean;
  hasUnreachable: boolean;
}

export interface AllVlansFlowLayout {
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
export function computeHopDistances(
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
    const assigned = Array.from(dist.keys()).sort((a, b) => (dist.get(a)! - dist.get(b)!));
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

function stackColumn<T extends { height: number; y: number }>(items: T[], totalHeight: number) {
  if (items.length === 0) return;
  const contentH = items.reduce((s, it) => s + it.height, 0) + CARD_GAP_Y * Math.max(0, items.length - 1);
  let y = TOP_HEADER_Y + Math.max(0, (totalHeight - TOP_HEADER_Y - PAD_BOTTOM - contentH) / 2);
  for (const it of items) {
    it.y = Math.round(y);
    y += it.height + CARD_GAP_Y;
  }
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

  // Collect participating devices.
  const participatingSet = new Set<string>(vlan.devices);
  for (const g of vlan.gateways) participatingSet.add(g.device_id);
  for (const h of vlan.hops) {
    participatingSet.add(h.from_id);
    participatingSet.add(h.to_id);
  }
  for (const ap of vlan.access_ports) participatingSet.add(ap.device_id);
  for (const s of vlan.ssids) participatingSet.add(s.device_id);

  const participating = Array.from(participatingSet);

  const hopAdj = new Map<string, Set<string>>();
  for (const h of vlan.hops) {
    addAdj(hopAdj, h.from_id, h.to_id);
  }
  const topoAdj = buildTopoAdj(topology);

  const gatewayDeviceIds = Array.from(new Set(vlan.gateways.map((g) => g.device_id)));
  const seeds = gatewayDeviceIds.length > 0
    ? gatewayDeviceIds
    : (topology?.root_ids ?? []).filter((id) => participatingSet.has(id));

  const hopDistances = computeHopDistances(participating, seeds, hopAdj, topoAdj, devicesById);

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

  // Group devices by hop distance.
  const byDist = new Map<number, string[]>();
  for (const devId of participating) {
    const d = hopDistances.get(devId) ?? 0;
    byDist.set(d, [...(byDist.get(d) ?? []), devId]);
  }
  const distLevels = Array.from(byDist.keys()).sort((a, b) => a - b);
  const maxDist = distLevels.length > 0 ? distLevels[distLevels.length - 1] : 0;

  // Order devices per distance tier using barycenter from previous tier.
  const prevOrder = new Map<string, number>();
  for (const d of distLevels) {
    const ids = byDist.get(d)!;
    const ordered = d === distLevels[0]
      ? [...ids].sort((a, b) => {
          const ga = gatewayDeviceIds.indexOf(a);
          const gb = gatewayDeviceIds.indexOf(b);
          if (ga !== -1 || gb !== -1) {
            return (ga === -1 ? 999 : ga) - (gb === -1 ? 999 : gb);
          }
          return displayName(devicesById, a).localeCompare(displayName(devicesById, b));
        })
      : orderByBarycenter(ids, prevOrder, hopAdj.size > 0 ? hopAdj : topoAdj, devicesById);
    byDist.set(d, ordered);
    ordered.forEach((id, idx) => prevOrder.set(id, idx));
  }

  const hasSegmentCol = vlan.gateways.length > 0 || foreignLeaks.length > 0;
  const deviceColOffset = hasSegmentCol ? 1 : 0;

  // Build endpoint groups per device (access ports + SSIDs, plus foreign leaked endpoints).
  const accessByDev = new Map<string, VLANFlowAccessPort[]>();
  for (const ap of vlan.access_ports) {
    accessByDev.set(ap.device_id, [...(accessByDev.get(ap.device_id) ?? []), ap]);
  }
  const ssidsByDev = new Map<string, VLANFlowSSID[]>();
  for (const s of vlan.ssids) {
    ssidsByDev.set(s.device_id, [...(ssidsByDev.get(s.device_id) ?? []), s]);
  }

  const endpointSpecs: {
    id: string;
    deviceId: string;
    vlanId: number;
    isForeignLeak: boolean;
    accessPorts: VLANFlowAccessPort[];
    ssids: VLANFlowSSID[];
    finding?: VLANFlowFinding;
  }[] = [];

  for (const d of distLevels) {
    for (const devId of byDist.get(d) ?? []) {
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

  // Add foreign leaked endpoint cards for leak findings (showing what foreign VLAN endpoints sit on the leaked peer).
  for (const lf of leakFindings) {
    const otherVid = lf.vlans.find((v) => v !== vlan.id);
    if (otherVid == null) continue;
    const foreignVlan = vlansById.get(otherVid);
    if (!foreignVlan) continue;
    const targetDev = lf.vlans[0] === vlan.id ? lf.peer_id : lf.device_id;
    const fAps = foreignVlan.access_ports.filter((ap) => ap.device_id === targetDev);
    const fSsids = foreignVlan.ssids.filter((s) => s.device_id === targetDev);
    if (fAps.length > 0 || fSsids.length > 0) {
      endpointSpecs.push({
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

  const hasEndpointCol = endpointSpecs.length > 0;

  // Build columns with X coordinates.
  const columns: FlowColumn[] = [];
  let cursorX = PAD_X;
  const distToColIdx = new Map<number, number>();

  if (hasSegmentCol) {
    columns.push({
      index: columns.length,
      kind: 'segments',
      label: '1. L3 ZONES & SEGMENTS',
      x: cursorX,
      width: SEGMENT_COL_W,
      nodeIds: [],
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
      label: deviceColumnLabel(colIdx + 1, d, vlan.gateways.length > 0, maxDist),
      x: cursorX,
      width: DEVICE_COL_W,
      nodeIds: byDist.get(d) ?? [],
    });
    cursorX += DEVICE_COL_W + COL_GAP;
  }

  const endpointColIdx = hasEndpointCol ? columns.length : -1;
  if (hasEndpointCol) {
    columns.push({
      index: endpointColIdx,
      kind: 'endpoints',
      label: `${endpointColIdx + 1}. ENDPOINTS`,
      x: cursorX,
      width: ENDPOINT_COL_W,
      nodeIds: endpointSpecs.map((e) => e.id),
    });
    cursorX += ENDPOINT_COL_W + PAD_X;
  } else {
    cursorX = Math.max(cursorX - COL_GAP + PAD_X, PAD_X * 2 + DEVICE_COL_W);
  }

  const devColMap = new Map<string, number>();
  for (const d of distLevels) {
    const cIdx = distToColIdx.get(d)!;
    for (const devId of byDist.get(d) ?? []) {
      devColMap.set(devId, cIdx);
    }
  }

  // Pre-count ports per device side to size device cards accurately.
  const leftPortCount = new Map<string, number>();
  const rightPortCount = new Map<string, number>();
  const inc = (m: Map<string, number>, k: string) => m.set(k, (m.get(k) ?? 0) + 1);

  for (const g of vlan.gateways) {
    inc(leftPortCount, g.device_id);
  }
  for (const h of vlan.hops) {
    const cFrom = devColMap.get(h.from_id) ?? deviceColOffset;
    const cTo = devColMap.get(h.to_id) ?? deviceColOffset;
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
  for (const ep of endpointSpecs) {
    inc(rightPortCount, ep.deviceId);
  }

  // Create segment cards.
  const segmentCards: FlowSegmentCard[] = [];
  if (hasSegmentCol) {
    const segCol = columns[0];
    vlan.gateways.forEach((gw, idx) => {
      const addrLines = Math.min(2, Math.max(1, gw.addresses.length));
      const h = 92 + addrLines * 16 + (gw.is_management ? 24 : 0);
      const id = `gw:${gw.device_id}:${gw.segment_id}:${idx}`;
      segmentCards.push({
        id,
        kind: 'gateway',
        vlanId: vlan.id,
        vlanName: vlan.name,
        gateway: gw,
        x: segCol.x,
        y: 0,
        width: segCol.width,
        height: h,
      });
      segCol.nodeIds.push(id);
    });
    for (const fl of foreignLeaks) {
      const fv = vlansById.get(fl.foreignVid);
      const id = `foreign:${fl.foreignVid}:${findingKey(fl.finding)}`;
      segmentCards.push({
        id,
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
      segCol.nodeIds.push(id);
    }
  }

  // Create device cards.
  const reachedSet = new Set(vlan.reached);
  const hasGateway = vlan.gateways.some((g) => g.addresses.length > 0);
  const deviceCardsById = new Map<string, FlowDeviceCard>();
  const deviceCardsByCol = new Map<number, FlowDeviceCard[]>();

  for (const d of distLevels) {
    const colIdx = distToColIdx.get(d)!;
    const col = columns[colIdx];
    const colCards: FlowDeviceCard[] = [];
    for (const devId of byDist.get(d) ?? []) {
      const maxPorts = Math.max(leftPortCount.get(devId) ?? 0, rightPortCount.get(devId) ?? 0, 1);
      const h = Math.max(114, 52 + maxPorts * 29 + 30);
      const card: FlowDeviceCard = {
        id: `dev:${devId}`,
        deviceId: devId,
        hopDistance: d,
        columnIndex: colIdx,
        reached: hasGateway ? reachedSet.has(devId) : true,
        hasGateway,
        x: col.x,
        y: 0,
        width: col.width,
        height: h,
        leftPorts: [],
        rightPorts: [],
        localAccessCount: (accessByDev.get(devId) ?? []).length,
        localSsidCount: (ssidsByDev.get(devId) ?? []).length,
      };
      deviceCardsById.set(devId, card);
      colCards.push(card);
    }
    deviceCardsByCol.set(colIdx, colCards);
  }

  // Create endpoint cards.
  const endpointCards: FlowEndpointCard[] = endpointSpecs.map((spec) => {
    const visibleItems = Math.min(4, spec.accessPorts.length) + Math.min(3, spec.ssids.length);
    const extraOverflow = (spec.accessPorts.length > 4 ? 1 : 0) + (spec.ssids.length > 3 ? 1 : 0);
    const h = Math.max(76, 46 + (visibleItems + extraOverflow) * 18 + (spec.isForeignLeak ? 18 : 0));
    const allUnreachable =
      !spec.isForeignLeak &&
      hasGateway &&
      spec.accessPorts.every((ap) => !ap.reachable) &&
      spec.ssids.every((s) => !s.reachable);
    const col = columns[endpointColIdx];
    return {
      id: spec.id,
      deviceId: spec.deviceId,
      vlanId: spec.vlanId,
      isForeignLeak: spec.isForeignLeak,
      accessPorts: spec.accessPorts,
      ssids: spec.ssids,
      allUnreachable,
      finding: spec.finding,
      x: col.x,
      y: 0,
      width: col.width,
      height: h,
    };
  });

  // Compute total height from the tallest column.
  const colHeight = (heights: number[]) =>
    heights.length === 0 ? 0 : heights.reduce((a, b) => a + b, 0) + CARD_GAP_Y * (heights.length - 1);

  const heights = [
    colHeight(segmentCards.map((c) => c.height)),
    ...Array.from(deviceCardsByCol.values()).map((cards) => colHeight(cards.map((c) => c.height))),
    colHeight(endpointCards.map((c) => c.height)),
  ];
  const totalHeight = Math.max(320, TOP_HEADER_Y + Math.max(...heights, 220) + PAD_BOTTOM);

  // Stack segment & device columns vertically.
  stackColumn(segmentCards, totalHeight);
  for (const cards of deviceCardsByCol.values()) {
    stackColumn(cards, totalHeight);
  }

  // Order endpoint cards by parent device Y-center so ribbons stay horizontal and crossing-free.
  endpointCards.sort((a, b) => {
    const da = deviceCardsById.get(a.deviceId);
    const db = deviceCardsById.get(b.deviceId);
    const ya = da ? da.y + da.height / 2 : 0;
    const yb = db ? db.y + db.height / 2 : 0;
    if (ya !== yb) return ya - yb;
    return Number(a.isForeignLeak) - Number(b.isForeignLeak);
  });
  stackColumn(endpointCards, totalHeight);

  // Now attach sorted port chips and build ribbons.
  type PortSlot = {
    side: 'left' | 'right';
    peerY: number;
    label: string;
    mode: VLANMode;
    state: HopState;
    hopEdgeId: string;
    isLeak: boolean;
    isDeadEndMissing: boolean;
    isMismatch: boolean;
    assignY: (y: number) => void;
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
      fromNodeId: sc.id,
      toNodeId: devCard.id,
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
      label: sc.gateway.parent_link || sc.gateway.segment_name,
      mode: 'tagged',
      state: 'carried',
      hopEdgeId: ribbon.id,
      isLeak: false,
      isDeadEndMissing: false,
      isMismatch: false,
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
    const sameCol = cFrom.columnIndex === cTo.columnIndex;
    const swap = sameCol ? cFrom.y > cTo.y : cFrom.columnIndex > cTo.columnIndex;

    const leftCard = swap ? cTo : cFrom;
    const rightCard = swap ? cFrom : cTo;
    const leftLink = (swap ? h.to_link : h.from_link) || (h.kind === 'wireless_uplink' ? 'mesh' : 'link');
    const rightLink = (swap ? h.from_link : h.to_link) || (h.kind === 'wireless_uplink' ? 'mesh' : 'link');
    const leftMode: VLANMode = swap ? h.to_mode : h.from_mode;
    const rightMode: VLANMode = swap ? h.from_mode : h.to_mode;

    const foreignVid = finding?.kind === 'leak' ? finding.vlans.find((v) => v !== vlan.id) : undefined;

    const ribbon: FlowRibbon = {
      id: `hop:${h.edge_id}`,
      kind: 'hop',
      edgeId: h.edge_id,
      hop: h,
      finding,
      fromNodeId: leftCard.id,
      toNodeId: rightCard.id,
      x1: leftCard.x + leftCard.width,
      y1: leftCard.y + leftCard.height / 2,
      x2: sameCol ? rightCard.x + rightCard.width : rightCard.x,
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
      label: leftLink,
      mode: leftMode,
      state: h.state,
      hopEdgeId: h.edge_id,
      isLeak: h.state === 'leak',
      isDeadEndMissing: h.state === 'dead_end' && leftMode === 'none',
      isMismatch: h.state === 'mismatch',
      assignY: (y) => {
        ribbon.y1 = y;
      },
    });
    addSlot(rightCard.deviceId, {
      side: sameCol ? 'right' : 'left',
      peerY: leftCard.y + leftCard.height / 2,
      label: rightLink,
      mode: rightMode,
      state: h.state,
      hopEdgeId: h.edge_id,
      isLeak: h.state === 'leak',
      isDeadEndMissing: h.state === 'dead_end' && rightMode === 'none',
      isMismatch: h.state === 'mismatch',
      assignY: (y) => {
        ribbon.y2 = y;
      },
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
      fromNodeId: devCard.id,
      toNodeId: ep.id,
      x1: devCard.x + devCard.width,
      y1: devCard.y + devCard.height / 2,
      x2: ep.x,
      y2: ep.y + ep.height / 2,
      sameColumn: false,
      linkKind: ep.ssids.length > 0 && ep.accessPorts.length === 0 ? 'wireless_uplink' : 'wired',
      fromMode: 'untagged',
      toMode: 'untagged',
      state: ep.isForeignLeak ? 'leak' : ep.allUnreachable ? 'dead_end' : 'carried',
      isForeignLeak: ep.isForeignLeak,
      foreignVlanId: ep.isForeignLeak ? ep.vlanId : undefined,
    };
    ribbons.push(ribbon);
    addSlot(devCard.deviceId, {
      side: 'right',
      peerY: ep.y + ep.height / 2,
      label: ep.isForeignLeak ? `leak:${ep.vlanId}` : ep.ssids.length > 0 && ep.accessPorts.length === 0 ? 'wifi' : 'access',
      mode: 'untagged',
      state: ribbon.state,
      hopEdgeId: ribbon.id,
      isLeak: ep.isForeignLeak,
      isDeadEndMissing: false,
      isMismatch: false,
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
      const railBottom = card.y + card.height - 26;
      const span = Math.max(0, railBottom - railTop);
      sideSlots.forEach((slot, idx) => {
        const y = sideSlots.length === 1
          ? Math.round(railTop + span / 2)
          : Math.round(railTop + (span * idx) / (sideSlots.length - 1));
        slot.assignY(y);
        // Only emit visible port chips for actual infrastructure hops (keep gateway/endpoint wires crisp).
        if (slot.hopEdgeId && !slot.hopEdgeId.startsWith('seg-link:') && !slot.hopEdgeId.startsWith('ep-link:')) {
          const chip: FlowPortChip = {
            id: `${card.deviceId}:${side}:${slot.hopEdgeId}:${idx}`,
            side,
            label: slot.label,
            mode: slot.mode,
            state: slot.state,
            hopEdgeId: slot.hopEdgeId,
            x: side === 'left' ? card.x + 6 : card.x + card.width - 6,
            y,
            isLeak: slot.isLeak,
            isDeadEndMissing: slot.isDeadEndMissing,
            isMismatch: slot.isMismatch,
          };
          if (side === 'left') card.leftPorts.push(chip);
          else card.rightPorts.push(chip);
        }
      });
    }
  }

  return {
    vlanId: vlan.id,
    width: Math.max(720, Math.round(cursorX)),
    height: Math.round(totalHeight),
    columns,
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

  const participating = Array.from(participatingSet);
  const topoAdj = buildTopoAdj(topology);
  const seeds = gatewayDeviceSet.size > 0
    ? Array.from(gatewayDeviceSet)
    : (topology?.root_ids ?? []).filter((id) => participatingSet.has(id));

  const hopDistances = computeHopDistances(participating, seeds, hopAdj, topoAdj, devicesById);

  const byDist = new Map<number, string[]>();
  for (const devId of participating) {
    const d = hopDistances.get(devId) ?? 0;
    byDist.set(d, [...(byDist.get(d) ?? []), devId]);
  }
  const distLevels = Array.from(byDist.keys()).sort((a, b) => a - b);
  const maxDist = distLevels.length > 0 ? distLevels[distLevels.length - 1] : 0;

  const prevOrder = new Map<string, number>();
  for (const d of distLevels) {
    const ids = byDist.get(d)!;
    const ordered = d === distLevels[0]
      ? [...ids].sort((a, b) => displayName(devicesById, a).localeCompare(displayName(devicesById, b)))
      : orderByBarycenter(ids, prevOrder, hopAdj.size > 0 ? hopAdj : topoAdj, devicesById);
    byDist.set(d, ordered);
    ordered.forEach((id, idx) => prevOrder.set(id, idx));
  }

  // Endpoint summaries per device.
  const endpointEntriesByDev = new Map<string, AllVlansEndpointEntry[]>();
  for (const v of vlans) {
    const vHasLeak = flow.findings.some((f) => f.kind === 'leak' && f.vlans.includes(v.id));
    const devsHere = new Set<string>([
      ...v.access_ports.map((ap) => ap.device_id),
      ...v.ssids.map((s) => s.device_id),
    ]);
    for (const devId of devsHere) {
      const aps = v.access_ports.filter((ap) => ap.device_id === devId);
      const ssids = v.ssids.filter((s) => s.device_id === devId);
      const devLeak = vHasLeak && flow.findings.some((f) => f.kind === 'leak' && f.vlans.includes(v.id) && (f.device_id === devId || f.peer_id === devId));
      const hasRoutedGw = v.gateways.some((g) => g.addresses.length > 0);
      const entry: AllVlansEndpointEntry = {
        vlanId: v.id,
        vlanName: v.name,
        accessCount: aps.length,
        unreachableAccessCount: hasRoutedGw ? aps.filter((ap) => !ap.reachable).length : 0,
        ssids: ssids.map((s) => ({ name: s.name, reachable: !hasRoutedGw || s.reachable })),
        hasLeak: devLeak,
      };
      endpointEntriesByDev.set(devId, [...(endpointEntriesByDev.get(devId) ?? []), entry]);
    }
  }

  const hasEndpointCol = endpointEntriesByDev.size > 0;
  const columns: FlowColumn[] = [];
  let cursorX = PAD_X;

  columns.push({
    index: 0,
    kind: 'vlans',
    label: '1. ALL VLANS & SEGMENTS',
    x: cursorX,
    width: SEGMENT_COL_W,
    nodeIds: vlans.map((v) => `vlan:${v.id}`),
  });
  cursorX += SEGMENT_COL_W + COL_GAP;

  const distToColIdx = new Map<number, number>();
  for (const d of distLevels) {
    const colIdx = columns.length;
    distToColIdx.set(d, colIdx);
    columns.push({
      index: colIdx,
      kind: 'devices',
      hopDistance: d,
      label: deviceColumnLabel(colIdx + 1, d, gatewayDeviceSet.size > 0, maxDist),
      x: cursorX,
      width: DEVICE_COL_W,
      nodeIds: byDist.get(d) ?? [],
    });
    cursorX += DEVICE_COL_W + COL_GAP;
  }

  const endpointColIdx = hasEndpointCol ? columns.length : -1;
  if (hasEndpointCol) {
    columns.push({
      index: endpointColIdx,
      kind: 'endpoints',
      label: `${endpointColIdx + 1}. ENDPOINTS`,
      x: cursorX,
      width: ENDPOINT_COL_W,
      nodeIds: Array.from(endpointEntriesByDev.keys()).map((d) => `all-ep:${d}`),
    });
    cursorX += ENDPOINT_COL_W + PAD_X;
  } else {
    cursorX = Math.max(cursorX - COL_GAP + PAD_X, PAD_X * 2 + SEGMENT_COL_W);
  }

  // Create VLAN cards in Col 0.
  const vlanCards: AllVlansVlanCard[] = vlans.map((v) => ({
    id: `vlan:${v.id}`,
    vlan: v,
    findings: flow.findings.filter((f) => f.vlans.includes(v.id)),
    hasUnverified: v.hops.some((h) => h.state === 'unverified'),
    x: columns[0].x,
    y: 0,
    width: columns[0].width,
    height: 78,
  }));
  const vlanCardById = new Map(vlanCards.map((c) => [c.vlan.id, c]));

  // Create device cards.
  const deviceCardsById = new Map<string, AllVlansDeviceCard>();
  const deviceCardsByCol = new Map<number, AllVlansDeviceCard[]>();
  for (const d of distLevels) {
    const colIdx = distToColIdx.get(d)!;
    const col = columns[colIdx];
    const cards: AllVlansDeviceCard[] = [];
    for (const devId of byDist.get(d) ?? []) {
      const vIds = Array.from(devVlans.get(devId) ?? []).sort((a, b) => a - b);
      const devFindings = flow.findings.filter((f) => f.device_id === devId || f.peer_id === devId);
      const card: AllVlansDeviceCard = {
        id: `dev:${devId}`,
        deviceId: devId,
        hopDistance: d,
        columnIndex: colIdx,
        vlanIds: vIds,
        findings: devFindings,
        x: col.x,
        y: 0,
        width: col.width,
        height: Math.max(106, 68 + Math.min(vIds.length, 6) * 14),
      };
      deviceCardsById.set(devId, card);
      cards.push(card);
    }
    deviceCardsByCol.set(colIdx, cards);
  }

  // Create endpoint summary cards.
  const endpointCards: AllVlansEndpointCard[] = [];
  if (hasEndpointCol) {
    const col = columns[endpointColIdx];
    for (const [devId, entries] of endpointEntriesByDev.entries()) {
      const visible = Math.min(entries.length, 5);
      const h = Math.max(82, 46 + visible * 18 + (entries.length > 5 ? 16 : 0));
      endpointCards.push({
        id: `all-ep:${devId}`,
        deviceId: devId,
        entries,
        hasUnreachable: entries.some((e) => e.unreachableAccessCount > 0 || e.ssids.some((s) => !s.reachable)),
        hasLeak: entries.some((e) => e.hasLeak),
        x: col.x,
        y: 0,
        width: col.width,
        height: h,
      });
    }
  }

  const colHeight = (heights: number[]) =>
    heights.length === 0 ? 0 : heights.reduce((a, b) => a + b, 0) + CARD_GAP_Y * (heights.length - 1);

  const heights = [
    colHeight(vlanCards.map((c) => c.height)),
    ...Array.from(deviceCardsByCol.values()).map((cards) => colHeight(cards.map((c) => c.height))),
    colHeight(endpointCards.map((c) => c.height)),
  ];
  const totalHeight = Math.max(340, TOP_HEADER_Y + Math.max(...heights, 240) + PAD_BOTTOM);

  stackColumn(vlanCards, totalHeight);
  for (const cards of deviceCardsByCol.values()) {
    stackColumn(cards, totalHeight);
  }
  endpointCards.sort((a, b) => {
    const ya = deviceCardsById.get(a.deviceId)?.y ?? 0;
    const yb = deviceCardsById.get(b.deviceId)?.y ?? 0;
    return ya - yb;
  });
  stackColumn(endpointCards, totalHeight);

  // Segment -> gateway device links (or first device for L2-only VLAN).
  const segmentLinks: AllVlansSegmentLink[] = [];
  for (const vc of vlanCards) {
    const targets = vc.vlan.gateways.length > 0
      ? Array.from(new Set(vc.vlan.gateways.map((g) => g.device_id)))
      : vc.vlan.devices.slice(0, 1);
    targets.forEach((devId) => {
      const dc = deviceCardsById.get(devId);
      if (!dc) return;
      segmentLinks.push({
        vlanId: vc.vlan.id,
        deviceId: devId,
        x1: vc.x + vc.width,
        y1: vc.y + vc.height / 2,
        x2: dc.x,
        y2: dc.y + dc.height / 2,
      });
    });
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
      l.y2 = links.length === 1 ? Math.round(top + span / 2) : Math.round(top + (span * i) / (links.length - 1));
    });
  }

  // Group hops by edge_id into multi-lane bundles.
  const bundleMap = new Map<string, AllVlansEdgeBundle>();
  for (const v of vlans) {
    for (const h of v.hops) {
      const cFrom = deviceCardsById.get(h.from_id);
      const cTo = deviceCardsById.get(h.to_id);
      if (!cFrom || !cTo) continue;

      const sameCol = cFrom.columnIndex === cTo.columnIndex;
      const swap = sameCol ? cFrom.y > cTo.y : cFrom.columnIndex > cTo.columnIndex;
      const leftCard = swap ? cTo : cFrom;
      const rightCard = swap ? cFrom : cTo;

      let bundle = bundleMap.get(h.edge_id);
      if (!bundle) {
        bundle = {
          edgeId: h.edge_id,
          fromDeviceId: leftCard.deviceId,
          toDeviceId: rightCard.deviceId,
          linkKind: h.kind,
          sameColumn: sameCol,
          x1: leftCard.x + leftCard.width,
          y1: leftCard.y + leftCard.height / 2,
          x2: sameCol ? rightCard.x + rightCard.width : rightCard.x,
          y2: rightCard.y + rightCard.height / 2,
          lanes: [],
          findings: flow.findings.filter((f) => f.edge_id === h.edge_id),
        };
        bundleMap.set(h.edge_id, bundle);
      }
      const laneFinding = bundle.findings.find((f) => f.vlans.includes(v.id));
      bundle.lanes.push({
        vlanId: v.id,
        state: h.state,
        fromMode: swap ? h.to_mode : h.from_mode,
        toMode: swap ? h.from_mode : h.to_mode,
        finding: laneFinding,
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
      b.y1 = Math.round(top + (span * i) / (bundles.length - 1));
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
    width: Math.max(720, Math.round(cursorX)),
    height: Math.round(totalHeight),
    columns,
    vlanCards,
    deviceCards: Array.from(deviceCardsById.values()),
    endpointCards,
    segmentLinks,
    edgeBundles,
    endpointLinks,
  };
}
