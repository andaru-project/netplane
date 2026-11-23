// SPDX-License-Identifier: AGPL-3.0-or-later
import { Text, useComputedColorScheme } from '@mantine/core';
import { type KeyboardEvent, useMemo } from 'react';
import {
  type Device,
  type TopologyGraph,
  type VLANFlow,
  type VLANFlowFinding,
  type VLANFlowVLAN,
  type VLANMode,
} from '../api';
import {
  computeAllVlansLayout,
  computeSingleVlanLayout,
  type FlowPortChip,
  type FlowRibbon,
} from '../vlanFlowLayout';
import {
  type ColorPair,
  deviceName,
  findingKey,
  getVlanColor,
  modeShort,
} from '../vlanUtils';

interface ThemePalette {
  isDark: boolean;
  canvas: string;
  surface: string;
  subtle: string;
  border: string;
  borderSoft: string;
  text: string;
  dim: string;
  green: string;
  amber: string;
  amberInk: string;
  red: string;
  blue: string;
  gray: string;
}

function palette(isDark: boolean): ThemePalette {
  return {
    isDark,
    canvas: isDark ? '#121417' : '#fbfcfd',
    surface: isDark ? '#1a1b1e' : '#ffffff',
    subtle: isDark ? '#25262b' : '#f1f3f5',
    border: isDark ? '#373a40' : '#ced4da',
    borderSoft: isDark ? '#2c2e33' : '#e9ecef',
    text: isDark ? '#e9ecef' : '#17212c',
    dim: isDark ? '#909296' : '#526171',
    green: isDark ? '#40c057' : '#2b8a3e',
    amber: isDark ? '#fab005' : '#e67700',
    amberInk: '#17212c',
    red: isDark ? '#ff6b6b' : '#c92a2a',
    blue: isDark ? '#4dabf7' : '#1971c2',
    gray: isDark ? '#787b82' : '#868e96',
  };
}

function bezierPath(x1: number, y1: number, x2: number, y2: number, sameColumn = false): string {
  if (sameColumn) {
    const loopX = Math.max(x1, x2) + 54;
    return `M ${x1} ${y1} C ${loopX} ${y1}, ${loopX} ${y2}, ${x2} ${y2}`;
  }
  const dx = Math.max(36, Math.abs(x2 - x1) * 0.46);
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
}

function truncate(text: string, max: number): string {
  if (text.length <= max) return text;
  return `${text.slice(0, max - 1)}…`;
}

function onActivate(fn: () => void) {
  return (e: KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      fn();
    }
  };
}

function PortChipSvg({
  chip,
  vColor,
  C,
}: {
  chip: FlowPortChip;
  vColor: string;
  C: ThemePalette;
}) {
  const label = truncate(chip.label, 10);
  const badgeW = 19;
  const textW = Math.max(34, label.length * 6.0 + 6);
  const totalW = textW + badgeW + 6;
  const startX = chip.side === 'left' ? chip.x : chip.x - totalW;
  const bx = chip.side === 'left' ? 3 : textW + 3;
  const tx = chip.side === 'left' ? badgeW + 7 : 5;

  let bFill = C.subtle;
  let bStroke = C.border;
  let bText = C.dim;
  let bDash: string | undefined;
  let bWidth = 1;
  let glyph = modeShort(chip.mode);
  let outerStroke = C.borderSoft;

  if (chip.isLeak) {
    bFill = C.red;
    bStroke = C.red;
    bText = '#ffffff';
    glyph = '⇄';
    outerStroke = C.red;
  } else if (chip.isDeadEndMissing) {
    bFill = C.isDark ? 'rgba(250,176,5,0.2)' : 'rgba(230,119,0,0.14)';
    bStroke = C.amber;
    bText = C.amber;
    glyph = '✕';
    outerStroke = C.amber;
  } else if (chip.mode === 'unknown') {
    bFill = C.subtle;
    bStroke = C.gray;
    bText = C.gray;
    bDash = '2,2';
    glyph = '?';
    outerStroke = C.gray;
  } else if (chip.mode === 'tagged') {
    bFill = vColor;
    bStroke = chip.isMismatch ? C.amber : vColor;
    bText = C.isDark ? '#101418' : '#ffffff';
    bWidth = chip.isMismatch ? 1.8 : 1;
    glyph = 'T';
    if (chip.isMismatch) outerStroke = C.amber;
  } else if (chip.mode === 'untagged') {
    bFill = C.surface;
    bStroke = chip.isMismatch ? C.amber : vColor;
    bText = chip.isMismatch ? C.amber : vColor;
    bDash = '3,1.5';
    bWidth = 1.6;
    glyph = 'U';
    if (chip.isMismatch) outerStroke = C.amber;
  } else {
    glyph = '✕';
  }

  return (
    <g transform={`translate(${startX}, ${chip.y - 11})`}>
      <rect
        width={totalW}
        height={22}
        rx={4}
        fill={C.subtle}
        stroke={outerStroke}
        strokeWidth={1}
        strokeDasharray={chip.mode === 'unknown' ? '3,2' : undefined}
      />
      <rect
        x={bx}
        y={2.5}
        width={badgeW}
        height={17}
        rx={3}
        fill={bFill}
        stroke={bStroke}
        strokeWidth={bWidth}
        strokeDasharray={bDash}
      />
      <text
        x={bx + badgeW / 2}
        y={14.5}
        textAnchor="middle"
        fill={bText}
        fontSize={10.5}
        fontWeight={800}
        fontFamily="ui-monospace, monospace"
      >
        {glyph}
      </text>
      <text
        x={tx}
        y={14.5}
        fill={C.text}
        fontSize={10}
        fontWeight={600}
        fontFamily="ui-monospace, monospace"
      >
        {label}
      </text>
    </g>
  );
}

function modePill(fromMode: VLANMode, toMode: VLANMode, linkKind: string): string {
  const m = fromMode === toMode ? modeShort(fromMode) : `${modeShort(fromMode)}→${modeShort(toMode)}`;
  if (linkKind === 'lag') return `LAG · ${m}`;
  if (linkKind === 'wireless_uplink') return `Wi-Fi · ${m}`;
  return m;
}

interface Props {
  mode: 'single' | 'all';
  flow: VLANFlow;
  selectedVlanId: number | null;
  selectedFindingKey: string | null;
  devicesById: Map<string, Device>;
  topology: TopologyGraph | null;
  vlanColors: Map<number, ColorPair>;
  onSelectVlan: (vlanId: number) => void;
  onSelectFinding: (finding: VLANFlowFinding) => void;
  onOpenDevice: (deviceId: string) => void;
}

export function VlanFlowCanvas({
  mode,
  flow,
  selectedVlanId,
  selectedFindingKey,
  devicesById,
  topology,
  vlanColors,
  onSelectVlan,
  onSelectFinding,
  onOpenDevice,
}: Props) {
  const isDark = useComputedColorScheme('light') === 'dark';
  const C = useMemo(() => palette(isDark), [isDark]);

  const activeVlan: VLANFlowVLAN | null = useMemo(() => {
    if (flow.vlans.length === 0) return null;
    return flow.vlans.find((v) => v.id === selectedVlanId) ?? flow.vlans[0];
  }, [flow.vlans, selectedVlanId]);

  const singleLayout = useMemo(() => {
    if (mode !== 'single' || !activeVlan) return null;
    return computeSingleVlanLayout(activeVlan, flow, devicesById, topology);
  }, [mode, activeVlan, flow, devicesById, topology]);

  const allLayout = useMemo(() => {
    if (mode !== 'all' || flow.vlans.length === 0) return null;
    return computeAllVlansLayout(flow, devicesById, topology);
  }, [mode, flow, devicesById, topology]);

  if (flow.vlans.length === 0) {
    return (
      <div style={{ padding: 36, textAlign: 'center' }}>
        <Text c="dimmed" size="sm">
          No VLANs discovered yet. Configure switches, routers, or APs with VLAN memberships and poll to trace flow.
        </Text>
      </div>
    );
  }

  if (mode === 'all' && allLayout) {
    return (
      <svg
        style={{ width: '100%', minWidth: Math.min(allLayout.width, 860), height: 'auto' }}
        viewBox={`0 0 ${allLayout.width} ${allLayout.height}`}
        role="img"
        aria-label="All VLANs propagation overview"
      >
        {/* Column headers */}
        {allLayout.columns.map((col) => (
          <g key={`col-${col.index}`}>
            <text
              x={col.x}
              y={22}
              fill={C.dim}
              fontSize={10.5}
              fontWeight={700}
              letterSpacing="0.06em"
              fontFamily="system-ui, sans-serif"
            >
              {col.label}
            </text>
            <line x1={col.x} y1={30} x2={col.x + col.width} y2={30} stroke={C.borderSoft} strokeWidth={1} />
          </g>
        ))}

        {/* Segment -> gateway ribbons */}
        {allLayout.segmentLinks.map((sl) => {
          const col = getVlanColor(sl.vlanId, vlanColors, isDark);
          return (
            <path
              key={`seg-${sl.vlanId}-${sl.deviceId}`}
              d={bezierPath(sl.x1, sl.y1, sl.x2, sl.y2)}
              fill="none"
              stroke={col}
              strokeWidth={2.6}
              style={{ cursor: 'pointer' }}
              onClick={() => onSelectVlan(sl.vlanId)}
            />
          );
        })}

        {/* Multi-lane edge bundles */}
        {allLayout.edgeBundles.map((bundle) => {
          const laneGap = Math.min(11, 44 / Math.max(bundle.lanes.length, 1));
          const midX = bundle.sameColumn
            ? Math.max(bundle.x1, bundle.x2) + 46
            : (bundle.x1 + bundle.x2) / 2;
          const midY = (bundle.y1 + bundle.y2) / 2;
          const topFinding = bundle.findings[0];
          const allUnverified = bundle.lanes.every((l) => l.state === 'unverified');

          return (
            <g key={`bundle-${bundle.edgeId}`}>
              {bundle.lanes.map((lane, idx) => {
                const offset = (idx - (bundle.lanes.length - 1) / 2) * laneGap;
                const y1 = bundle.y1 + offset;
                const y2 = bundle.y2 + offset;
                const col = getVlanColor(lane.vlanId, vlanColors, isDark);

                if (lane.state === 'dead_end') {
                  const stopX = bundle.sameColumn ? midX : bundle.x1 + (bundle.x2 - bundle.x1) * 0.5;
                  const stopY = (y1 + y2) / 2;
                  return (
                    <g
                      key={`${bundle.edgeId}-v${lane.vlanId}`}
                      style={{ cursor: 'pointer' }}
                      onClick={() => (lane.finding ? onSelectFinding(lane.finding) : onSelectVlan(lane.vlanId))}
                    >
                      <path d={bezierPath(bundle.x1, y1, stopX - 4, stopY)} fill="none" stroke={col} strokeWidth={2.4} />
                      <path
                        d={bezierPath(stopX + 4, stopY, bundle.x2, y2)}
                        fill="none"
                        stroke={C.amber}
                        strokeWidth={1.8}
                        strokeDasharray="4,3"
                      />
                      <line x1={stopX} y1={stopY - 7} x2={stopX} y2={stopY + 7} stroke={C.amber} strokeWidth={2.6} />
                    </g>
                  );
                }

                const strokeColor =
                  lane.state === 'leak'
                    ? C.red
                    : lane.state === 'unverified'
                      ? C.gray
                      : col;
                const dash =
                  lane.state === 'unverified'
                    ? '3,4'
                    : bundle.linkKind === 'wireless_uplink'
                      ? '5,4'
                      : lane.state === 'mismatch'
                        ? '6,3'
                        : undefined;
                return (
                  <path
                    key={`${bundle.edgeId}-v${lane.vlanId}`}
                    d={bezierPath(bundle.x1, y1, bundle.x2, y2, bundle.sameColumn)}
                    fill="none"
                    stroke={strokeColor}
                    strokeWidth={lane.state === 'leak' ? 3.2 : 2.3}
                    strokeDasharray={dash}
                    style={{ cursor: 'pointer' }}
                    onClick={() => (lane.finding ? onSelectFinding(lane.finding) : onSelectVlan(lane.vlanId))}
                  />
                );
              })}

              {/* Overlay pill for bundle findings or unverified wireless uplink */}
              {topFinding ? (
                <g
                  style={{ cursor: 'pointer' }}
                  role="button"
                  tabIndex={0}
                  aria-label={topFinding.message}
                  onClick={() => onSelectFinding(topFinding)}
                  onKeyDown={onActivate(() => onSelectFinding(topFinding))}
                >
                  {(() => {
                    const label =
                      topFinding.kind === 'leak'
                        ? `⚠ LEAK ${topFinding.vlans[0]}⇄${topFinding.vlans[1]}`
                        : topFinding.kind === 'tag_mismatch'
                          ? `⇄ V${topFinding.vlans[0]} MISMATCH`
                          : `⊘ V${topFinding.vlans[0]} DROP`;
                    const pw = Math.max(82, label.length * 6.0 + 14);
                    const bg = topFinding.severity === 'crit' ? C.red : C.amber;
                    const fg = topFinding.severity === 'crit' ? '#ffffff' : C.amberInk;
                    return (
                      <>
                        <rect x={midX - pw / 2} y={midY - 10} width={pw} height={20} rx={4} fill={bg} stroke={C.surface} strokeWidth={1.2} />
                        <text
                          x={midX}
                          y={midY + 3.5}
                          textAnchor="middle"
                          fill={fg}
                          fontSize={9.5}
                          fontWeight={800}
                          fontFamily="ui-monospace, monospace"
                        >
                          {label}
                        </text>
                      </>
                    );
                  })()}
                </g>
              ) : allUnverified ? (
                <g>
                  <rect
                    x={midX - 44}
                    y={midY - 9.5}
                    width={88}
                    height={19}
                    rx={4}
                    fill={C.surface}
                    stroke={C.gray}
                    strokeWidth={1.2}
                    strokeDasharray="3,2"
                  />
                  <text
                    x={midX}
                    y={midY + 3.5}
                    textAnchor="middle"
                    fill={C.gray}
                    fontSize={9}
                    fontWeight={700}
                    fontFamily="ui-monospace, monospace"
                  >
                    ? UNVERIFIED
                  </text>
                </g>
              ) : null}
            </g>
          );
        })}

        {/* Device -> endpoint links */}
        {allLayout.endpointLinks.map((el) => (
          <path
            key={`ep-l-${el.deviceId}`}
            d={bezierPath(el.x1, el.y1, el.x2, el.y2)}
            fill="none"
            stroke={el.hasLeak ? C.red : el.hasUnreachable ? C.amber : C.border}
            strokeWidth={el.hasLeak ? 2.4 : 1.8}
            strokeDasharray={el.hasUnreachable ? '4,3' : undefined}
          />
        ))}

        {/* Col 0: VLAN cards */}
        {allLayout.vlanCards.map((vc) => {
          const col = getVlanColor(vc.vlan.id, vlanColors, isDark);
          const hasLeak = vc.findings.some((f) => f.kind === 'leak');
          const hasDead = vc.findings.some((f) => f.kind === 'dead_end');
          const hasMis = vc.findings.some((f) => f.kind === 'tag_mismatch');
          const gw = vc.vlan.gateways[0];
          const addr = gw?.addresses[0] ? `${gw.addresses[0].address}/${gw.addresses[0].prefix_length}` : 'L2 only';
          const zone = gw?.zone_name || gw?.zone_id;
          const statusText = hasLeak
            ? '⚠ LEAK'
            : hasDead
              ? '⊘ DEAD-END'
              : hasMis
                ? '⇄ MISMATCH'
                : vc.hasUnverified
                  ? '? UNVERIFIED'
                  : '✓ OK';
          const statusCol = hasLeak
            ? C.red
            : hasDead || hasMis
              ? C.amber
              : vc.hasUnverified
                ? C.gray
                : C.green;

          return (
            <g
              key={vc.id}
              transform={`translate(${vc.x}, ${vc.y})`}
              style={{ cursor: 'pointer' }}
              role="button"
              tabIndex={0}
              aria-label={`Focus VLAN ${vc.vlan.id}`}
              onClick={() => onSelectVlan(vc.vlan.id)}
              onKeyDown={onActivate(() => onSelectVlan(vc.vlan.id))}
            >
              <rect width={vc.width} height={vc.height} rx={6} fill={C.surface} stroke={C.border} strokeWidth={1.2} />
              <rect width={5} height={vc.height} rx={2} fill={col} />
              <text x={14} y={23} fill={C.text} fontSize={12.5} fontWeight={700} fontFamily="system-ui, sans-serif">
                {truncate(`VLAN ${vc.vlan.id}${vc.vlan.name ? ` · ${vc.vlan.name}` : ''}`, 24)}
              </text>
              <text x={14} y={41} fill={C.dim} fontSize={10.5} fontFamily="ui-monospace, monospace">
                {truncate(addr, 26)}
              </text>
              <text x={14} y={60} fill={statusCol} fontSize={10} fontWeight={700} fontFamily="ui-monospace, monospace">
                {truncate(`${statusText}${zone ? ` · ${zone}` : ''}`, 27)}
              </text>
            </g>
          );
        })}

        {/* Device cards */}
        {allLayout.deviceCards.map((dc) => {
          const dev = devicesById.get(dc.deviceId);
          const dName = dev?.name || dc.deviceId;
          const hasLeak = dc.findings.some((f) => f.kind === 'leak');
          const hasWarn = dc.findings.some((f) => f.kind !== 'leak');
          const stroke = hasLeak ? C.red : hasWarn ? C.amber : C.border;
          return (
            <g
              key={dc.id}
              transform={`translate(${dc.x}, ${dc.y})`}
              style={{ cursor: 'pointer' }}
              role="button"
              tabIndex={0}
              aria-label={`Open device ${dName}`}
              onClick={() => onOpenDevice(dc.deviceId)}
              onKeyDown={onActivate(() => onOpenDevice(dc.deviceId))}
            >
              <rect width={dc.width} height={dc.height} rx={6} fill={C.surface} stroke={stroke} strokeWidth={hasLeak || hasWarn ? 1.6 : 1.2} />
              <rect width={dc.width} height={34} rx={6} fill={C.subtle} />
              <text x={12} y={16} fill={C.text} fontSize={12} fontWeight={700} fontFamily="system-ui, sans-serif">
                {truncate(dName, 22)}
              </text>
              <text x={12} y={29} fill={C.dim} fontSize={9.5} fontFamily="ui-monospace, monospace">
                {truncate([dev?.driver, dev?.mgmt_ip].filter(Boolean).join(' · ') || dc.deviceId, 28)}
              </text>
              <text x={12} y={54} fill={C.dim} fontSize={10} fontFamily="ui-monospace, monospace">
                {truncate(`VLANs: ${dc.vlanIds.join(', ')}`, 28)}
              </text>
              <text
                x={12}
                y={dc.height - 14}
                fill={hasLeak ? C.red : hasWarn ? C.amber : C.green}
                fontSize={10}
                fontWeight={700}
                fontFamily="ui-monospace, monospace"
              >
                {hasLeak
                  ? `⚠ ${dc.findings.filter((f) => f.kind === 'leak').length} leak finding(s)`
                  : hasWarn
                    ? `⊘ ${dc.findings.length} warning(s)`
                    : `✓ ${dc.vlanIds.length} VLAN${dc.vlanIds.length === 1 ? '' : 's'}`}
              </text>
            </g>
          );
        })}

        {/* Endpoint summary cards */}
        {allLayout.endpointCards.map((ep) => {
          const dName = deviceName(devicesById, ep.deviceId);
          return (
            <g
              key={ep.id}
              transform={`translate(${ep.x}, ${ep.y})`}
              style={{ cursor: 'pointer' }}
              role="button"
              tabIndex={0}
              aria-label={`Inspect endpoints on ${dName}`}
              onClick={() => onOpenDevice(ep.deviceId)}
              onKeyDown={onActivate(() => onOpenDevice(ep.deviceId))}
            >
              <rect
                width={ep.width}
                height={ep.height}
                rx={6}
                fill={C.surface}
                stroke={ep.hasLeak ? C.red : ep.hasUnreachable ? C.amber : C.border}
                strokeWidth={ep.hasLeak || ep.hasUnreachable ? 1.5 : 1.2}
                strokeDasharray={ep.hasUnreachable && !ep.hasLeak ? '5,3' : undefined}
              />
              <text x={12} y={21} fill={C.text} fontSize={11.5} fontWeight={700} fontFamily="system-ui, sans-serif">
                {truncate(`${dName} Endpoints`, 25)}
              </text>
              {ep.entries.slice(0, 5).map((ent, idx) => {
                const unreach = ent.unreachableAccessCount > 0 || ent.ssids.some((s) => !s.reachable);
                const parts: string[] = [];
                if (ent.accessCount > 0) parts.push(`${ent.accessCount}p`);
                if (ent.ssids.length > 0) parts.push(`${ent.ssids.length} SSID`);
                const prefix = ent.hasLeak ? '⚠ ' : unreach ? '⊘ ' : '';
                return (
                  <text
                    key={`${ep.id}-v${ent.vlanId}`}
                    x={12}
                    y={41 + idx * 18}
                    fill={ent.hasLeak ? C.red : unreach ? C.amber : C.dim}
                    fontSize={10}
                    fontWeight={ent.hasLeak || unreach ? 700 : 500}
                    fontFamily="ui-monospace, monospace"
                  >
                    {truncate(`${prefix}V${ent.vlanId}: ${parts.join(' · ')}`, 30)}
                  </text>
                );
              })}
              {ep.entries.length > 5 && (
                <text x={12} y={41 + 5 * 18} fill={C.dim} fontSize={9.5} fontFamily="ui-monospace, monospace">
                  +{ep.entries.length - 5} more VLAN(s)
                </text>
              )}
            </g>
          );
        })}
      </svg>
    );
  }

  if (!singleLayout || !activeVlan) return null;

  const vColor = getVlanColor(activeVlan.id, vlanColors, isDark);

  // Collect leak gradients needed for this VLAN.
  const leakGradients: { id: string; foreignColor: string }[] = [];
  const seenGrads = new Set<string>();
  for (const r of singleLayout.ribbons) {
    if (r.state === 'leak' && r.foreignVlanId != null) {
      const gid = `np-grad-leak-${activeVlan.id}-${r.foreignVlanId}`;
      if (!seenGrads.has(gid)) {
        seenGrads.add(gid);
        leakGradients.push({ id: gid, foreignColor: getVlanColor(r.foreignVlanId, vlanColors, isDark) });
      }
    }
  }

  const renderRibbonPaths = (r: FlowRibbon) => {
    const d = bezierPath(r.x1, r.y1, r.x2, r.y2, r.sameColumn);
    const isSelFinding = r.finding != null && findingKey(r.finding) === selectedFindingKey;
    const handleClick = r.finding ? () => onSelectFinding(r.finding!) : undefined;

    if (r.kind === 'segment_link') {
      return (
        <g key={`path-${r.id}`}>
          <path d={d} fill="none" stroke={vColor} strokeWidth={3.2} strokeLinecap="round" />
        </g>
      );
    }

    if (r.kind === 'endpoint_link') {
      if (r.isForeignLeak) {
        return (
          <g key={`path-${r.id}`} style={{ cursor: handleClick ? 'pointer' : undefined }} onClick={handleClick}>
            <path d={d} fill="none" stroke={C.red} strokeWidth={4} strokeLinecap="round" />
            <path d={d} fill="none" stroke={C.canvas} strokeWidth={1.6} />
          </g>
        );
      }
      if (r.state === 'dead_end') {
        return (
          <g key={`path-${r.id}`}>
            <path d={d} fill="none" stroke={C.amber} strokeWidth={2.2} strokeDasharray="5,4" opacity={0.8} />
          </g>
        );
      }
      return (
        <g key={`path-${r.id}`}>
          <path
            d={d}
            fill="none"
            stroke={vColor}
            strokeWidth={r.linkKind === 'wireless_uplink' ? 2.6 : 4.6}
            strokeDasharray={r.linkKind === 'wireless_uplink' ? '5,4' : undefined}
            strokeLinecap="round"
          />
          {r.linkKind !== 'wireless_uplink' && <path d={d} fill="none" stroke={C.canvas} strokeWidth={1.8} />}
        </g>
      );
    }

    // Infrastructure hop ribbon
    if (r.state === 'leak') {
      const gid = r.foreignVlanId != null ? `np-grad-leak-${activeVlan.id}-${r.foreignVlanId}` : undefined;
      return (
        <g key={`path-${r.id}`} style={{ cursor: handleClick ? 'pointer' : undefined }} onClick={handleClick}>
          <path d={d} fill="none" stroke="transparent" strokeWidth={20} />
          {isSelFinding && <path d={d} fill="none" stroke="#ffb21c" strokeWidth={10} strokeOpacity={0.45} />}
          <path d={d} fill="none" stroke={gid ? `url(#${gid})` : C.red} strokeWidth={6.4} strokeLinecap="round" />
          <path d={d} fill="none" stroke={C.canvas} strokeWidth={2} strokeDasharray="5,5" />
        </g>
      );
    }

    if (r.state === 'dead_end') {
      const stopX = r.sameColumn ? Math.max(r.x1, r.x2) + 46 : r.x1 + (r.x2 - r.x1) * 0.5;
      const stopY = (r.y1 + r.y2) / 2;
      const fromCarries = r.fromMode !== 'none';
      const dBefore = bezierPath(r.x1, r.y1, stopX - 6, stopY);
      const dAfter = bezierPath(stopX + 6, stopY, r.x2, r.y2);
      return (
        <g key={`path-${r.id}`} style={{ cursor: handleClick ? 'pointer' : undefined }} onClick={handleClick}>
          <path d={d} fill="none" stroke="transparent" strokeWidth={18} />
          {isSelFinding && <path d={d} fill="none" stroke="#ffb21c" strokeWidth={8} strokeOpacity={0.4} />}
          <path
            d={dBefore}
            fill="none"
            stroke={fromCarries ? vColor : C.amber}
            strokeWidth={fromCarries ? 3.2 : 2}
            strokeDasharray={fromCarries ? undefined : '4,4'}
            opacity={fromCarries ? 1 : 0.72}
          />
          <path
            d={dAfter}
            fill="none"
            stroke={fromCarries ? C.amber : vColor}
            strokeWidth={fromCarries ? 2 : 3.2}
            strokeDasharray={fromCarries ? '4,4' : undefined}
            opacity={fromCarries ? 0.72 : 1}
          />
          <line x1={stopX} y1={stopY - 12} x2={stopX} y2={stopY + 12} stroke={C.amber} strokeWidth={3.2} strokeLinecap="round" />
        </g>
      );
    }

    if (r.state === 'mismatch') {
      return (
        <g key={`path-${r.id}`} style={{ cursor: handleClick ? 'pointer' : undefined }} onClick={handleClick}>
          <path d={d} fill="none" stroke="transparent" strokeWidth={18} />
          {isSelFinding && <path d={d} fill="none" stroke="#ffb21c" strokeWidth={8} strokeOpacity={0.4} />}
          <path d={d} fill="none" stroke={C.amber} strokeWidth={4} strokeDasharray="7,4" strokeLinecap="round" />
        </g>
      );
    }

    if (r.state === 'unverified') {
      return (
        <g key={`path-${r.id}`}>
          <path d={d} fill="none" stroke={C.gray} strokeWidth={2.6} strokeDasharray="3,4" strokeLinecap="round" />
        </g>
      );
    }

    // Carried hop
    if (r.linkKind === 'lag') {
      return (
        <g key={`path-${r.id}`}>
          <path d={d} fill="none" stroke={vColor} strokeWidth={6.5} strokeLinecap="round" />
          <path d={d} fill="none" stroke={C.canvas} strokeWidth={2} />
        </g>
      );
    }
    if (r.fromMode === 'untagged' && r.toMode === 'untagged') {
      return (
        <g key={`path-${r.id}`}>
          <path d={d} fill="none" stroke={vColor} strokeWidth={5.2} strokeLinecap="round" />
          <path d={d} fill="none" stroke={C.canvas} strokeWidth={2.2} />
        </g>
      );
    }
    if (r.linkKind === 'wireless_uplink') {
      return (
        <g key={`path-${r.id}`}>
          <path d={d} fill="none" stroke={vColor} strokeWidth={2.8} strokeDasharray="6,4" strokeLinecap="round" />
        </g>
      );
    }
    return (
      <g key={`path-${r.id}`}>
        <path d={d} fill="none" stroke={vColor} strokeWidth={3.2} strokeLinecap="round" />
      </g>
    );
  };

  const renderRibbonOverlay = (r: FlowRibbon) => {
    if (r.kind === 'segment_link') return null;

    const mx = r.sameColumn ? Math.max(r.x1, r.x2) + 46 : (r.x1 + r.x2) / 2;
    const my = (r.y1 + r.y2) / 2;
    const handleClick = r.finding ? () => onSelectFinding(r.finding!) : undefined;

    if (r.kind === 'endpoint_link') {
      const label = r.isForeignLeak
        ? `LEAK → ${r.foreignVlanId}`
        : r.state === 'dead_end'
          ? 'STRANDED'
          : r.linkKind === 'wireless_uplink'
            ? 'Wi-Fi'
            : 'U';
      const pw = Math.max(26, label.length * 6.0 + 12);
      const color = r.isForeignLeak ? C.red : r.state === 'dead_end' ? C.amber : vColor;
      return (
        <g key={`ov-${r.id}`} style={{ cursor: handleClick ? 'pointer' : undefined }} onClick={handleClick}>
          <rect x={mx - pw / 2} y={my - 9.5} width={pw} height={19} rx={4} fill={C.surface} stroke={color} strokeWidth={1.3} />
          <text x={mx} y={my + 3.5} textAnchor="middle" fill={color} fontSize={9.5} fontWeight={700} fontFamily="ui-monospace, monospace">
            {label}
          </text>
        </g>
      );
    }

    if (r.state === 'leak') {
      const lbl = r.finding ? `⚠ LEAK ${r.finding.vlans[0]}→${r.finding.vlans[1]}` : '⚠ LEAK';
      const pw = Math.max(96, lbl.length * 6.2 + 16);
      return (
        <g
          key={`ov-${r.id}`}
          style={{ cursor: handleClick ? 'pointer' : undefined }}
          role={handleClick ? 'button' : undefined}
          tabIndex={handleClick ? 0 : undefined}
          aria-label={r.finding?.message || 'VLAN leak'}
          onClick={handleClick}
          onKeyDown={handleClick ? onActivate(handleClick) : undefined}
        >
          <rect x={mx - pw / 2} y={my - 11} width={pw} height={22} rx={4} fill={C.red} stroke={C.surface} strokeWidth={1.2} />
          <text x={mx} y={my + 3.5} textAnchor="middle" fill="#ffffff" fontSize={9.5} fontWeight={800} fontFamily="ui-monospace, monospace">
            {lbl}
          </text>
        </g>
      );
    }

    if (r.state === 'dead_end') {
      const pw = 88;
      return (
        <g
          key={`ov-${r.id}`}
          style={{ cursor: handleClick ? 'pointer' : undefined }}
          role={handleClick ? 'button' : undefined}
          tabIndex={handleClick ? 0 : undefined}
          aria-label={r.finding?.message || 'VLAN dead-end'}
          onClick={handleClick}
          onKeyDown={handleClick ? onActivate(handleClick) : undefined}
        >
          <rect x={mx - pw / 2} y={my - 28} width={pw} height={19} rx={4} fill={C.amber} />
          <text x={mx} y={my - 15} textAnchor="middle" fill={C.amberInk} fontSize={9.5} fontWeight={800} fontFamily="ui-monospace, monospace">
            ⊘ DEAD-END
          </text>
        </g>
      );
    }

    if (r.state === 'mismatch') {
      const lbl = `⇄ ${modeShort(r.fromMode)}≠${modeShort(r.toMode)}`;
      const pw = Math.max(76, lbl.length * 6.4 + 14);
      return (
        <g
          key={`ov-${r.id}`}
          style={{ cursor: handleClick ? 'pointer' : undefined }}
          role={handleClick ? 'button' : undefined}
          tabIndex={handleClick ? 0 : undefined}
          aria-label={r.finding?.message || 'VLAN tag mismatch'}
          onClick={handleClick}
          onKeyDown={handleClick ? onActivate(handleClick) : undefined}
        >
          <rect x={mx - pw / 2} y={my - 10} width={pw} height={20} rx={4} fill={C.amber} />
          <text x={mx} y={my + 3.5} textAnchor="middle" fill={C.amberInk} fontSize={9.5} fontWeight={800} fontFamily="ui-monospace, monospace">
            {lbl}
          </text>
        </g>
      );
    }

    if (r.state === 'unverified') {
      const lbl = r.linkKind === 'wireless_uplink' ? '? Wi-Fi' : '? UNVERIFIED';
      const pw = Math.max(66, lbl.length * 6.0 + 14);
      return (
        <g key={`ov-${r.id}`}>
          <rect
            x={mx - pw / 2}
            y={my - 9.5}
            width={pw}
            height={19}
            rx={4}
            fill={C.surface}
            stroke={C.gray}
            strokeWidth={1.3}
            strokeDasharray="3,2"
          />
          <text x={mx} y={my + 3.5} textAnchor="middle" fill={C.gray} fontSize={9.5} fontWeight={700} fontFamily="ui-monospace, monospace">
            {lbl}
          </text>
        </g>
      );
    }

    const lbl = modePill(r.fromMode, r.toMode, r.linkKind);
    const pw = Math.max(28, lbl.length * 6.0 + 12);
    return (
      <g key={`ov-${r.id}`}>
        <rect x={mx - pw / 2} y={my - 9.5} width={pw} height={19} rx={4} fill={C.surface} stroke={vColor} strokeWidth={1.3} />
        <text x={mx} y={my + 3.5} textAnchor="middle" fill={vColor} fontSize={9.5} fontWeight={700} fontFamily="ui-monospace, monospace">
          {lbl}
        </text>
      </g>
    );
  };

  return (
    <svg
      style={{ width: '100%', minWidth: Math.min(singleLayout.width, 860), height: 'auto' }}
      viewBox={`0 0 ${singleLayout.width} ${singleLayout.height}`}
      role="img"
      aria-label={`VLAN ${activeVlan.id}${activeVlan.name ? ` ${activeVlan.name}` : ''} flow diagram`}
    >
      <defs>
        <pattern
          id="np-hatch-unreach"
          width="8"
          height="8"
          patternUnits="userSpaceOnUse"
          patternTransform="rotate(45)"
        >
          <line x1="0" y1="0" x2="0" y2="8" stroke={C.amber} strokeWidth="1.5" strokeOpacity="0.24" />
        </pattern>
        {leakGradients.map((g) => (
          <linearGradient key={g.id} id={g.id} x1="0%" y1="0%" x2="100%" y2="0%">
            <stop offset="0%" stopColor={vColor} />
            <stop offset="46%" stopColor={C.red} />
            <stop offset="54%" stopColor={C.red} />
            <stop offset="100%" stopColor={g.foreignColor} />
          </linearGradient>
        ))}
      </defs>

      {/* Column headers */}
      {singleLayout.columns.map((col) => (
        <g key={`scol-${col.index}`}>
          <text
            x={col.x}
            y={22}
            fill={C.dim}
            fontSize={10.5}
            fontWeight={700}
            letterSpacing="0.06em"
            fontFamily="system-ui, sans-serif"
          >
            {col.label}
          </text>
          <line x1={col.x} y1={30} x2={col.x + col.width} y2={30} stroke={C.borderSoft} strokeWidth={1} />
        </g>
      ))}

      {/* Ribbons (under cards) */}
      {singleLayout.ribbons.map(renderRibbonPaths)}

      {/* Segment / Zone cards */}
      {singleLayout.segmentCards.map((sc) => {
        if (sc.kind === 'foreign_leak' && sc.finding) {
          const fColor = getVlanColor(sc.vlanId, vlanColors, isDark);
          const fZone = sc.gateway?.zone_name || sc.gateway?.zone_id;
          const fAddr = sc.gateway?.addresses[0]
            ? `${sc.gateway.addresses[0].address}/${sc.gateway.addresses[0].prefix_length}`
            : null;
          return (
            <g
              key={sc.id}
              transform={`translate(${sc.x}, ${sc.y})`}
              style={{ cursor: 'pointer' }}
              role="button"
              tabIndex={0}
              aria-label={`Inspect foreign VLAN ${sc.vlanId} leak`}
              onClick={() => onSelectFinding(sc.finding!)}
              onKeyDown={onActivate(() => onSelectFinding(sc.finding!))}
            >
              <rect
                width={sc.width}
                height={sc.height}
                rx={6}
                fill={C.isDark ? 'rgba(255,107,107,0.10)' : 'rgba(201,42,42,0.06)'}
                stroke={C.red}
                strokeWidth={1.6}
                strokeDasharray="5,3"
              />
              <rect width={5} height={sc.height} rx={2} fill={fColor} />
              <text x={14} y={21} fill={C.red} fontSize={9.5} fontWeight={800} fontFamily="ui-monospace, monospace">
                ⚠ FOREIGN VLAN LEAKED
              </text>
              <text x={14} y={40} fill={C.text} fontSize={13} fontWeight={700} fontFamily="system-ui, sans-serif">
                {truncate(`VLAN ${sc.vlanId}${sc.vlanName ? ` · ${sc.vlanName}` : ''}`, 24)}
              </text>
              {(fZone || fAddr) && (
                <text x={14} y={58} fill={C.dim} fontSize={10} fontFamily="ui-monospace, monospace">
                  {truncate([fZone ? `zone:${fZone}` : '', fAddr || ''].filter(Boolean).join(' · '), 28)}
                </text>
              )}
              <text x={14} y={78} fill={C.red} fontSize={10} fontWeight={600} fontFamily="system-ui, sans-serif">
                Cross-VLAN untagged bridge
              </text>
              <text x={14} y={96} fill={C.dim} fontSize={9.5} fontFamily="ui-monospace, monospace">
                {truncate(`${deviceName(devicesById, sc.finding.device_id)}:${sc.finding.link}`, 28)}
              </text>
            </g>
          );
        }

        const gw = sc.gateway!;
        const gwDev = devicesById.get(gw.device_id);
        const zoneLabel = gw.zone_name || gw.zone_id;
        return (
          <g
            key={sc.id}
            transform={`translate(${sc.x}, ${sc.y})`}
            style={{ cursor: 'pointer' }}
            role="button"
            tabIndex={0}
            aria-label={`Open gateway ${gwDev?.name || gw.device_id}`}
            onClick={() => onOpenDevice(gw.device_id)}
            onKeyDown={onActivate(() => onOpenDevice(gw.device_id))}
          >
            <rect width={sc.width} height={sc.height} rx={6} fill={C.surface} stroke={vColor} strokeWidth={1.6} />
            <rect width={5} height={sc.height} rx={2} fill={vColor} />
            <text x={14} y={22} fill={vColor} fontSize={10} fontWeight={800} fontFamily="ui-monospace, monospace">
              {truncate(zoneLabel ? `ZONE: ${zoneLabel.toUpperCase()}` : 'L3 SEGMENT', 26)}
            </text>
            <text x={14} y={41} fill={C.text} fontSize={13.5} fontWeight={700} fontFamily="system-ui, sans-serif">
              {truncate(`VLAN ${sc.vlanId}${sc.vlanName ? ` · ${sc.vlanName}` : ''}`, 23)}
            </text>
            <text x={14} y={58} fill={C.dim} fontSize={10.5} fontFamily="ui-monospace, monospace">
              {truncate(`${gw.segment_name} (${gwDev?.name || gw.device_id})`, 27)}
            </text>
            <line x1={14} y1={68} x2={sc.width - 14} y2={68} stroke={C.borderSoft} />
            {gw.addresses.slice(0, 2).map((a, idx) => (
              <text key={`${a.address}/${a.prefix_length}`} x={14} y={85 + idx * 16} fill={C.text} fontSize={10.5} fontFamily="ui-monospace, monospace">
                {truncate(`${a.address}/${a.prefix_length}`, 26)}
              </text>
            ))}
            {gw.addresses.length === 0 && (
              <text x={14} y={85} fill={C.dim} fontSize={10.5} fontFamily="ui-monospace, monospace">
                No L3 IP configured
              </text>
            )}
            {gw.is_management && (
              <g transform={`translate(14, ${sc.height - 25})`}>
                <rect width={84} height={17} rx={3} fill={C.subtle} stroke={vColor} />
                <text x={42} y={12} textAnchor="middle" fill={vColor} fontSize={9.5} fontWeight={700} fontFamily="ui-monospace, monospace">
                  MANAGEMENT
                </text>
              </g>
            )}
          </g>
        );
      })}

      {/* Device cards + port chips */}
      {singleLayout.deviceCards.map((dc) => {
        const dev = devicesById.get(dc.deviceId);
        const dName = dev?.name || dc.deviceId;
        const devFindings = flow.findings.filter(
          (f) => f.vlans.includes(activeVlan.id) && (f.device_id === dc.deviceId || f.peer_id === dc.deviceId),
        );
        const hasLeak = devFindings.some((f) => f.kind === 'leak');
        const unreached = dc.hasGateway && !dc.reached;
        const stroke = hasLeak ? C.red : unreached ? C.amber : C.border;

        const footerParts: string[] = [];
        if (dc.localAccessCount > 0) footerParts.push(`${dc.localAccessCount} access`);
        if (dc.localSsidCount > 0) footerParts.push(`${dc.localSsidCount} SSID`);

        return (
          <g key={dc.id}>
            <g
              transform={`translate(${dc.x}, ${dc.y})`}
              style={{ cursor: 'pointer' }}
              role="button"
              tabIndex={0}
              aria-label={`Open device ${dName}`}
              onClick={() => onOpenDevice(dc.deviceId)}
              onKeyDown={onActivate(() => onOpenDevice(dc.deviceId))}
            >
              <rect
                width={dc.width}
                height={dc.height}
                rx={6}
                fill={unreached ? 'url(#np-hatch-unreach)' : C.surface}
                stroke={stroke}
                strokeWidth={hasLeak || unreached ? 1.7 : 1.3}
                strokeDasharray={unreached && !hasLeak ? '5,3' : undefined}
              />
              <rect width={dc.width} height={34} rx={6} fill={C.subtle} />
              <text x={12} y={16} fill={C.text} fontSize={12} fontWeight={700} fontFamily="system-ui, sans-serif">
                {truncate(dName, 22)}
              </text>
              <text x={12} y={29} fill={C.dim} fontSize={9.5} fontFamily="ui-monospace, monospace">
                {truncate([dev?.driver, dev?.mgmt_ip].filter(Boolean).join(' · ') || dc.deviceId, 28)}
              </text>
              <text
                x={12}
                y={dc.height - 10}
                fill={hasLeak ? C.red : unreached ? C.amber : C.dim}
                fontSize={9.5}
                fontWeight={hasLeak || unreached ? 700 : 500}
                fontFamily="ui-monospace, monospace"
              >
                {hasLeak
                  ? '⚠ CROSS-VLAN LEAK'
                  : unreached
                    ? `⊘ UNREACHABLE ON V${activeVlan.id}`
                    : footerParts.length > 0
                      ? footerParts.join(' · ')
                      : dc.hasGateway
                        ? '✓ Reaches gateway'
                        : 'L2 transit'}
              </text>
            </g>

            {dc.leftPorts.map((chip) => (
              <PortChipSvg key={chip.id} chip={chip} vColor={vColor} C={C} />
            ))}
            {dc.rightPorts.map((chip) => (
              <PortChipSvg key={chip.id} chip={chip} vColor={vColor} C={C} />
            ))}
          </g>
        );
      })}

      {/* Endpoint cards */}
      {singleLayout.endpointCards.map((ep) => {
        const dName = deviceName(devicesById, ep.deviceId);
        const handleClick = ep.finding
          ? () => onSelectFinding(ep.finding!)
          : () => onOpenDevice(ep.deviceId);
        const lines: { text: string; color: string }[] = [];
        for (const ap of ep.accessPorts.slice(0, 4)) {
          lines.push({
            text: `[U] ${ap.link}${ap.label ? ` · ${ap.label}` : ''}`,
            color: ep.allUnreachable ? C.dim : C.text,
          });
        }
        if (ep.accessPorts.length > 4) {
          lines.push({ text: `+${ep.accessPorts.length - 4} more access port(s)`, color: C.dim });
        }
        for (const s of ep.ssids.slice(0, 3)) {
          lines.push({
            text: `SSID ${s.name} (${s.client_count} cl)`,
            color: !s.reachable && activeVlan.gateways.length > 0 ? C.amber : C.text,
          });
        }
        if (ep.ssids.length > 3) {
          lines.push({ text: `+${ep.ssids.length - 3} more SSID(s)`, color: C.dim });
        }

        const fill = ep.isForeignLeak
          ? C.isDark
            ? 'rgba(255,107,107,0.10)'
            : 'rgba(201,42,42,0.06)'
          : ep.allUnreachable
            ? 'url(#np-hatch-unreach)'
            : C.surface;
        const stroke = ep.isForeignLeak ? C.red : ep.allUnreachable ? C.amber : vColor;
        const title = ep.isForeignLeak
          ? `⚠ LEAKED V${ep.vlanId} ON ${dName}`
          : ep.allUnreachable
            ? `⊘ Stranded · ${dName}`
            : `${dName} Endpoints`;

        return (
          <g
            key={ep.id}
            transform={`translate(${ep.x}, ${ep.y})`}
            style={{ cursor: 'pointer' }}
            role="button"
            tabIndex={0}
            aria-label={title}
            onClick={handleClick}
            onKeyDown={onActivate(handleClick)}
          >
            <rect
              width={ep.width}
              height={ep.height}
              rx={6}
              fill={fill}
              stroke={stroke}
              strokeWidth={ep.isForeignLeak || ep.allUnreachable ? 1.5 : 1.3}
              strokeDasharray={ep.allUnreachable && !ep.isForeignLeak ? '5,3' : undefined}
            />
            <text
              x={12}
              y={21}
              fill={ep.isForeignLeak ? C.red : ep.allUnreachable ? C.amber : C.text}
              fontSize={11}
              fontWeight={700}
              fontFamily="system-ui, sans-serif"
            >
              {truncate(title, 28)}
            </text>
            {lines.map((ln, idx) => (
              <text
                key={`${ep.id}-ln-${idx}`}
                x={12}
                y={41 + idx * 18}
                fill={ln.color}
                fontSize={10}
                fontFamily="ui-monospace, monospace"
              >
                {truncate(ln.text, 31)}
              </text>
            ))}
          </g>
        );
      })}

      {/* Midpoint pill overlays (topmost layer) */}
      {singleLayout.ribbons.map(renderRibbonOverlay)}
    </svg>
  );
}
