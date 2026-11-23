// SPDX-License-Identifier: AGPL-3.0-or-later
import {
  Badge,
  Button,
  Card,
  Group,
  SegmentedControl,
  Stack,
  Text,
  Title,
  useComputedColorScheme,
} from '@mantine/core';
import { useMemo } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import {
  type Device,
  type NetworkState,
  type TopologyGraph,
  type VLANFlow,
  type VLANFlowFinding,
} from '../api';
import { FindingBadge } from '../components/Badges';
import { VlanFlowCanvas } from '../components/VlanFlowCanvas';
import { devicePath, vlanFlowPath } from '../routing';
import {
  type ColorPair,
  describeFinding,
  deviceName,
  findHopForFinding,
  findingKey,
  getVlanColor,
  modeShort,
} from '../vlanUtils';

interface Props {
  flow: VLANFlow | null;
  state: NetworkState | null;
  topology: TopologyGraph | null;
  devicesById: Map<string, Device>;
  vlanColors: Map<number, ColorPair>;
}

export function VlanFlowScreen({ flow, state, topology, devicesById, vlanColors }: Props) {
  const isDark = useComputedColorScheme('light') === 'dark';
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();

  const vlans = flow?.vlans ?? [];
  const findings = flow?.findings ?? [];

  const mode: 'single' | 'all' = searchParams.get('mode') === 'all' ? 'all' : 'single';
  const rawVlan = searchParams.get('vlan');
  const parsedVlan = rawVlan ? Number(rawVlan) : null;
  const activeVlanId =
    parsedVlan != null && vlans.some((v) => v.id === parsedVlan)
      ? parsedVlan
      : vlans[0]?.id ?? null;

  const selectedFindingParam = searchParams.get('finding');

  const activeFinding: VLANFlowFinding | null = useMemo(() => {
    if (selectedFindingParam) {
      const exact = findings.find((f) => findingKey(f) === selectedFindingParam);
      if (exact) return exact;
    }
    if (mode === 'single' && activeVlanId != null) {
      return findings.find((f) => f.vlans.includes(activeVlanId)) ?? null;
    }
    return findings[0] ?? null;
  }, [findings, selectedFindingParam, mode, activeVlanId]);

  const activeVlan = vlans.find((v) => v.id === activeVlanId) ?? null;

  const updateParams = (next: { mode?: 'single' | 'all'; vlanId?: number | null; finding?: string | null }) => {
    const params = new URLSearchParams();
    const m = next.mode ?? mode;
    if (m === 'all') params.set('mode', 'all');
    const vid = next.vlanId !== undefined ? next.vlanId : activeVlanId;
    if (m === 'single' && vid != null) params.set('vlan', String(vid));
    if (next.finding) params.set('finding', next.finding);
    setSearchParams(params, { replace: true });
  };

  const handleSelectVlan = (vid: number) => {
    const firstForVlan = findings.find((f) => f.vlans.includes(vid));
    updateParams({ mode: 'single', vlanId: vid, finding: firstForVlan ? findingKey(firstForVlan) : null });
  };

  const handleSelectFinding = (f: VLANFlowFinding) => {
    const keepCurrentVlan = mode === 'single' && activeVlanId != null && f.vlans.includes(activeVlanId);
    updateParams({
      mode: 'single',
      vlanId: keepCurrentVlan ? activeVlanId : f.vlans[0],
      finding: findingKey(f),
    });
  };

  const leakCount = findings.filter((f) => f.kind === 'leak').length;
  const deadCount = findings.filter((f) => f.kind === 'dead_end').length;
  const mismatchCount = findings.filter((f) => f.kind === 'tag_mismatch').length;

  const activeFindingDesc = activeFinding && flow
    ? describeFinding(activeFinding, flow, state, devicesById)
    : null;
  const activeFindingHop = activeFinding && flow ? findHopForFinding(flow, activeFinding) : null;

  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-end" wrap="wrap" gap="sm">
        <div>
          <Title order={2} size="h3">VLAN Flow</Title>
          <Text size="sm" c="dimmed">
            Port-by-port VLAN propagation across trunks, LAGs, and wireless uplinks—highlighting dead-ends, mismatches, and cross-VLAN leaks.
          </Text>
        </div>
        <SegmentedControl
          size="xs"
          aria-label="VLAN Flow mode"
          value={mode}
          onChange={(v) => updateParams({ mode: v === 'all' ? 'all' : 'single', finding: selectedFindingParam })}
          data={[
            { label: 'Single VLAN', value: 'single' },
            { label: 'All VLANs', value: 'all' },
          ]}
        />
      </Group>

      {/* VLAN Selector Pill Bar */}
      <Card withBorder radius="md" p="sm">
        <Group justify="space-between" wrap="wrap" gap="xs">
          <Group gap={6} wrap="wrap" role="group" aria-label="Select VLAN">
            {vlans.map((v) => {
              const vFindings = findings.filter((f) => f.vlans.includes(v.id));
              const hasLeak = vFindings.some((f) => f.kind === 'leak');
              const hasDead = vFindings.some((f) => f.kind === 'dead_end');
              const hasMis = vFindings.some((f) => f.kind === 'tag_mismatch');
              const hasUnver = v.hops.some((h) => h.state === 'unverified');
              const isActive = mode === 'single' && activeVlanId === v.id;
              return (
                <button
                  key={v.id}
                  type="button"
                  className={`np-vlan-pill${isActive ? ' active' : ''}`}
                  aria-pressed={isActive}
                  onClick={() => handleSelectVlan(v.id)}
                >
                  <span
                    className="np-vlan-swatch"
                    style={{ background: getVlanColor(v.id, vlanColors, isDark) }}
                  />
                  <span className="np-mono">{v.id}</span>
                  {v.name && <span>{v.name}</span>}
                  {hasLeak ? (
                    <Badge size="xs" variant="light" color="red">⚠ Leak</Badge>
                  ) : hasDead ? (
                    <Badge size="xs" variant="light" color="yellow">⊘ Dead-end</Badge>
                  ) : hasMis ? (
                    <Badge size="xs" variant="light" color="orange">⇄ Mismatch</Badge>
                  ) : hasUnver ? (
                    <Badge size="xs" variant="outline" color="gray">?</Badge>
                  ) : (
                    <Badge size="xs" variant="light" color="green">✓</Badge>
                  )}
                </button>
              );
            })}
            {vlans.length === 0 && (
              <Text size="xs" c="dimmed">No VLANs reported by /api/v1/vlans/flow yet.</Text>
            )}
          </Group>

          <Group gap={6}>
            {leakCount > 0 && <Badge size="xs" variant="light" color="red">{leakCount} Leak</Badge>}
            {deadCount > 0 && <Badge size="xs" variant="light" color="yellow">{deadCount} Dead-end</Badge>}
            {mismatchCount > 0 && <Badge size="xs" variant="light" color="orange">{mismatchCount} Mismatch</Badge>}
            {findings.length === 0 && vlans.length > 0 && (
              <Badge size="xs" variant="light" color="green">All VLANs clean</Badge>
            )}
          </Group>
        </Group>
      </Card>

      {/* Full-width Flow Diagram */}
      <Card withBorder radius="md" p={0}>
        <div className="np-svg-stage">
          {flow ? (
            <VlanFlowCanvas
              mode={mode}
              flow={flow}
              selectedVlanId={activeVlanId}
              selectedFindingKey={activeFinding ? findingKey(activeFinding) : null}
              devicesById={devicesById}
              topology={topology}
              vlanColors={vlanColors}
              onSelectVlan={handleSelectVlan}
              onSelectFinding={handleSelectFinding}
              onOpenDevice={(devId) => void navigate(devicePath(devId))}
            />
          ) : (
            <div style={{ padding: 36, textAlign: 'center' }}>
              <Text size="sm" c="dimmed">Loading VLAN flow…</Text>
            </div>
          )}
        </div>
        <div className="np-legend-bar">
          <span className="np-legend-item"><span className="np-tag-chip t">T</span> Tagged (solid)</span>
          <span className="np-legend-item"><span className="np-tag-chip u">U</span> Untagged / PVID (double-rail)</span>
          <span className="np-legend-item">
            <svg width="24" height="10" aria-hidden="true">
              <line x1="0" y1="3" x2="24" y2="3" stroke="var(--np-primary)" strokeWidth="2" />
              <line x1="0" y1="7" x2="24" y2="7" stroke="var(--np-primary)" strokeWidth="2" />
            </svg>
            LAG Trunk
          </span>
          <span className="np-legend-item">
            <span className="np-tag-chip unverified">?</span> Unverified (wireless / unreported)
          </span>
          <span className="np-legend-item"><Badge size="xs" variant="light" color="yellow">⊘ Dead-End</Badge> Missing on peer</span>
          <span className="np-legend-item"><Badge size="xs" variant="light" color="orange">⇄ Mismatch</Badge> Tagged vs Untagged</span>
          <span className="np-legend-item"><Badge size="xs" variant="light" color="red">⚠ Leak</Badge> PVID mismatch</span>
        </div>
      </Card>

      {/* Consequence Inspector + All Findings */}
      <div className="np-grid-2-asym">
        <Card withBorder radius="md" p={0}>
          <Group
            justify="space-between"
            px="md"
            py="sm"
            wrap="wrap"
            gap="xs"
            style={{ borderBottom: '1px solid var(--np-border-subtle)' }}
          >
            <Title order={4} size="h5">Consequence Inspector</Title>
            {activeFinding ? (
              <FindingBadge finding={activeFinding} />
            ) : (
              <Badge size="xs" variant="light" color="green">✓ Clean End-to-End</Badge>
            )}
          </Group>

          <Stack gap="sm" p="md">
            {activeFinding && activeFindingDesc ? (
              <>
                <Text fw={700} size="sm">{activeFindingDesc.title}</Text>
                <Text size="sm">{activeFinding.message}</Text>

                {/* Side-by-side port comparison */}
                {(() => {
                  const nameA = deviceName(devicesById, activeFinding.device_id);
                  const nameB = deviceName(devicesById, activeFinding.peer_id);
                  const hopMatchesOrder = activeFindingHop
                    ? activeFindingHop.from_id === activeFinding.device_id &&
                      (activeFindingHop.from_id !== activeFindingHop.to_id ||
                        activeFindingHop.from_link === activeFinding.link)
                    : true;
                  const modeA = activeFindingHop
                    ? hopMatchesOrder
                      ? activeFindingHop.from_mode
                      : activeFindingHop.to_mode
                    : null;
                  const modeB = activeFindingHop
                    ? hopMatchesOrder
                      ? activeFindingHop.to_mode
                      : activeFindingHop.from_mode
                    : null;
                  const pvidA = activeFindingHop
                    ? hopMatchesOrder
                      ? activeFindingHop.from_pvid
                      : activeFindingHop.to_pvid
                    : undefined;
                  const taggedA = activeFindingHop
                    ? hopMatchesOrder
                      ? activeFindingHop.from_tagged
                      : activeFindingHop.to_tagged
                    : [];
                  const pvidB = activeFindingHop
                    ? hopMatchesOrder
                      ? activeFindingHop.to_pvid
                      : activeFindingHop.from_pvid
                    : undefined;
                  const taggedB = activeFindingHop
                    ? hopMatchesOrder
                      ? activeFindingHop.to_tagged
                      : activeFindingHop.from_tagged
                    : [];
                  const reportedA = pvidA != null || taggedA.length > 0;
                  const reportedB = pvidB != null || taggedB.length > 0;

                  return (
                    <div className="np-port-compare">
                      <div className="np-port-compare-side">
                        <Text size="xs" fw={700} ff="monospace">
                          {nameA}{activeFinding.link ? `:${activeFinding.link}` : ''}
                        </Text>
                        {modeA && (
                          <Text size="xs" ff="monospace" mt={4}>
                            VLAN {activeFinding.vlans[0]} mode: <strong>{modeA} ({modeShort(modeA)})</strong>
                          </Text>
                        )}
                        {reportedA && (
                          <>
                            <Text size="xs" ff="monospace" mt={2}>
                              PVID: <strong>{pvidA ?? '—'}</strong>
                            </Text>
                            <Text size="xs" ff="monospace">
                              Tagged: <strong>{taggedA.length > 0 ? taggedA.join(', ') : 'none'}</strong>
                            </Text>
                          </>
                        )}
                      </div>
                      <div className="np-port-compare-side">
                        <Text size="xs" fw={700} ff="monospace">
                          {nameB}{activeFinding.peer_link ? `:${activeFinding.peer_link}` : ''}
                        </Text>
                        {modeB && (
                          <Text size="xs" ff="monospace" mt={4}>
                            VLAN {activeFinding.vlans[0]} mode: <strong>{modeB} ({modeShort(modeB)})</strong>
                          </Text>
                        )}
                        {reportedB && (
                          <>
                            <Text size="xs" ff="monospace" mt={2}>
                              PVID: <strong>{pvidB ?? '—'}</strong>
                            </Text>
                            <Text size="xs" ff="monospace">
                              Tagged: <strong>{taggedB.length > 0 ? taggedB.join(', ') : 'none'}</strong>
                            </Text>
                          </>
                        )}
                      </div>
                    </div>
                  );
                })()}

                <div className={`np-callout ${activeFinding.severity}`}>
                  <Text size="10px" fw={700} tt="uppercase" c="dimmed" mb={2}>Consequence</Text>
                  <Text size="xs">{activeFindingDesc.consequence}</Text>
                </div>

                {activeFindingDesc.affected.length > 0 && (
                  <div>
                    <Text size="10px" fw={700} tt="uppercase" c="dimmed" mb={4}>Affected Devices & Endpoints</Text>
                    <ul style={{ margin: 0, paddingLeft: 18 }}>
                      {activeFindingDesc.affected.map((item) => (
                        <li key={item} className="np-mono" style={{ fontSize: 12, marginBottom: 2 }}>
                          {item}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                <div className="np-callout">
                  <Text size="10px" fw={700} tt="uppercase" c="dimmed" mb={2}>Suggested Fix</Text>
                  <Text size="xs">{activeFindingDesc.remediation}</Text>
                </div>

                <Group gap="xs" wrap="wrap">
                  <Button
                    size="compact-xs"
                    variant="default"
                    component={Link}
                    to={devicePath(activeFinding.device_id, activeFinding.link)}
                  >
                    Inspect {deviceName(devicesById, activeFinding.device_id)}
                    {activeFinding.link ? `:${activeFinding.link}` : ''} →
                  </Button>
                  <Button
                    size="compact-xs"
                    variant="default"
                    component={Link}
                    to={devicePath(activeFinding.peer_id, activeFinding.peer_link)}
                  >
                    Inspect {deviceName(devicesById, activeFinding.peer_id)}
                    {activeFinding.peer_link ? `:${activeFinding.peer_link}` : ''} →
                  </Button>
                </Group>
              </>
            ) : activeVlan ? (
              <>
                <Text fw={700} size="sm">
                  VLAN {activeVlan.id}{activeVlan.name ? ` · ${activeVlan.name}` : ''}
                </Text>
                <Text size="sm" c="dimmed">
                  No dead-ends, tag mismatches, or cross-VLAN leaks detected for VLAN {activeVlan.id}.
                  {activeVlan.hops.some((h) => h.state === 'unverified')
                    ? ' Some hops (such as wireless uplinks) have unverified VLAN membership.'
                    : ''}
                </Text>
                <Group gap="md" wrap="wrap">
                  <div>
                    <Text size="10px" fw={700} tt="uppercase" c="dimmed">Gateways</Text>
                    <Text size="xs" ff="monospace">
                      {activeVlan.gateways.length > 0
                        ? activeVlan.gateways
                            .map((g) => `${g.segment_name}${g.zone_name ? ` (${g.zone_name})` : ''}`)
                            .join(', ')
                        : 'L2 only (no L3 gateway)'}
                    </Text>
                  </div>
                  <div>
                    <Text size="10px" fw={700} tt="uppercase" c="dimmed">Devices Reached</Text>
                    <Text size="xs" ff="monospace">
                      {activeVlan.gateways.length > 0
                        ? `${activeVlan.reached.length} / ${activeVlan.devices.length}`
                        : `${activeVlan.devices.length} configured`}
                    </Text>
                  </div>
                  <div>
                    <Text size="10px" fw={700} tt="uppercase" c="dimmed">Endpoints</Text>
                    <Text size="xs" ff="monospace">
                      {activeVlan.access_ports.length} access · {activeVlan.ssids.length} SSID · {activeVlan.clients} clients
                    </Text>
                  </div>
                </Group>
              </>
            ) : (
              <Text size="sm" c="dimmed">Select a VLAN or finding to inspect its propagation consequences.</Text>
            )}
          </Stack>
        </Card>

        <Card withBorder radius="md" p={0}>
          <Group
            justify="space-between"
            px="md"
            py="sm"
            style={{ borderBottom: '1px solid var(--np-border-subtle)' }}
          >
            <Title order={4} size="h5">All Detected Findings ({findings.length})</Title>
          </Group>
          <div>
            {findings.map((f) => {
              const fk = findingKey(f);
              const isSel = activeFinding != null && findingKey(activeFinding) === fk;
              const desc = flow ? describeFinding(f, flow, state, devicesById) : null;
              return (
                <div
                  key={fk}
                  className={`np-alert-row ${f.severity}${isSel ? ' selected' : ''}`}
                  style={{ cursor: 'pointer' }}
                  role="button"
                  tabIndex={0}
                  onClick={() => handleSelectFinding(f)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      handleSelectFinding(f);
                    }
                  }}
                >
                  <div>
                    <Group gap="xs" mb={2} wrap="wrap">
                      <FindingBadge finding={f} />
                      <Text size="xs" fw={700}>{desc?.shortLabel ?? f.kind}</Text>
                    </Group>
                    <Text size="xs" c="dimmed" ff="monospace">
                      {deviceName(devicesById, f.device_id)}:{f.link} ↔ {deviceName(devicesById, f.peer_id)}:{f.peer_link}
                    </Text>
                  </div>
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    component={Link}
                    to={vlanFlowPath({ mode: 'single', vlanId: f.vlans[0], findingKey: fk })}
                    onClick={(e) => e.stopPropagation()}
                  >
                    Focus
                  </Button>
                </div>
              );
            })}
            {findings.length === 0 && (
              <div className="np-alert-row">
                <Text size="sm" c="dimmed">No VLAN leaks, dead-ends, or tag mismatches detected.</Text>
              </div>
            )}
          </div>
        </Card>
      </div>
    </Stack>
  );
}
