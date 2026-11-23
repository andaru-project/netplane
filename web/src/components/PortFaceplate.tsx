// SPDX-License-Identifier: AGPL-3.0-or-later
import {
  Badge,
  Button,
  Card,
  Group,
  Paper,
  Progress,
  SimpleGrid,
  Stack,
  Text,
  Tooltip,
  useComputedColorScheme,
} from '@mantine/core';
import { useEffect, useMemo, useState } from 'react';
import {
  type Device,
  formatBps,
  formatSpeed,
  type LagGroup,
  type Link,
  type NetworkState,
  type TopologyGraph,
  type VLANFlow,
  type VLANFlowFinding,
  type VLANFlowVLAN,
} from '../api';
import {
  type ColorPair,
  describeFinding,
  findingKey,
  findPeerForLink,
  findingsForPort,
  getVlanColor,
  isStpBlocked,
} from '../vlanUtils';
import { VlanChip } from './Badges';

interface Props {
  device: Device;
  links: Link[];
  lagGroups?: LagGroup[];
  flow?: VLANFlow | null;
  state?: NetworkState | null;
  topology?: TopologyGraph | null;
  devicesById?: Map<string, Device>;
  vlanColors?: Map<number, ColorPair>;
  selectedPort?: string | null;
  onSelectPort?: (ifName: string) => void;
  onOpenDevice?: (deviceId: string, portName?: string | null) => void;
  onOpenVlanFlow?: (vlanId: number, findingId?: string | null) => void;
}

export function PortFaceplate({
  device,
  links,
  lagGroups = [],
  flow = null,
  state = null,
  topology = null,
  devicesById = new Map(),
  vlanColors = new Map(),
  selectedPort = null,
  onSelectPort,
  onOpenDevice,
  onOpenVlanFlow,
}: Props) {
  const isDark = useComputedColorScheme('light') === 'dark';
  const sortedLinks = useMemo(() => [...links].sort((a, b) => a.index - b.index), [links]);
  const [internalPort, setInternalPort] = useState<string | null>(selectedPort ?? sortedLinks[0]?.if_name ?? null);
  const [hlVlan, setHlVlan] = useState<number | null>(null);

  useEffect(() => {
    if (selectedPort) {
      setInternalPort(selectedPort);
    } else if (sortedLinks.length > 0 && !sortedLinks.some((l) => l.if_name === internalPort)) {
      setInternalPort(sortedLinks[0].if_name);
    }
  }, [selectedPort, sortedLinks, internalPort]);

  const activePortName = selectedPort ?? internalPort;
  const selected = sortedLinks.find((l) => l.if_name === activePortName) ?? sortedLinks[0] ?? null;

  const selectPort = (ifName: string) => {
    setInternalPort(ifName);
    onSelectPort?.(ifName);
  };

  const vlansById = useMemo(
    () => new Map<number, VLANFlowVLAN>((flow?.vlans ?? []).map((v) => [v.id, v])),
    [flow],
  );

  const deviceVlans = useMemo(() => {
    const ids = new Set<number>();
    for (const l of sortedLinks) {
      if (l.pvid != null) ids.add(l.pvid);
      for (const t of l.tagged_vlans) ids.add(t);
    }
    return Array.from(ids).sort((a, b) => a - b);
  }, [sortedLinks]);

  const findings = flow?.findings ?? [];

  if (sortedLinks.length === 0) {
    return (
      <Card withBorder radius="md" p="md">
        <Text c="dimmed" size="sm">No physical links reported for this device.</Text>
      </Card>
    );
  }

  const lagMemberSet = new Set(lagGroups.flatMap((g) => g.member_links));
  const copperLinks = sortedLinks.filter((l) => !l.is_fiber && !lagMemberSet.has(l.if_name));
  const standaloneFiber = sortedLinks.filter((l) => l.is_fiber && !lagMemberSet.has(l.if_name));
  const upCount = sortedLinks.filter((l) => l.link_up).length;

  const renderTile = (l: Link) => {
    const isSel = selected?.if_name === l.if_name;
    const stpBlock = isStpBlocked(l.stp_state);
    const portAnoms = findingsForPort(findings, device.id, l.if_name, l.lag_group_id);
    const hasDomWarn = l.sfp?.present && (l.sfp.alert_state === 'warn' || l.sfp.alert_state === 'crit');

    const classes = ['np-port-tile'];
    if (l.is_fiber) classes.push('fiber');
    if (!l.admin_up) classes.push('disabled');
    else if (stpBlock) classes.push('stp-block');
    else if (l.link_up) classes.push('up');
    else classes.push('down');
    if (isSel) classes.push('selected');

    let vlanBadge: { mode: 'U' | 'T'; vid: number } | null = null;
    if (hlVlan != null) {
      if (l.pvid === hlVlan) {
        classes.push('vlan-match');
        vlanBadge = { mode: 'U', vid: hlVlan };
      } else if (l.tagged_vlans.includes(hlVlan)) {
        classes.push('vlan-match');
        vlanBadge = { mode: 'T', vid: hlVlan };
      } else {
        classes.push('vlan-dim');
      }
    }

    return (
      <Tooltip
        key={l.if_name}
        label={`${l.if_name} (${l.media})${l.label ? ` — ${l.label}` : ''} · ${l.link_up ? formatSpeed(l.speed_mbps) : l.oper_state}`}
      >
        <button
          type="button"
          className={classes.join(' ')}
          onClick={() => selectPort(l.if_name)}
          aria-pressed={isSel}
        >
          <span className="np-port-name">{l.if_name}</span>
          <span className={`np-port-jack${l.is_fiber ? ' fiber-cage' : ''}`}>
            <span className="np-port-led" />
          </span>
          <span className="np-port-speed">
            {!l.admin_up ? 'OFF' : stpBlock ? 'STP' : l.link_up ? formatSpeed(l.speed_mbps) : 'DOWN'}
          </span>
          <span className="np-port-tags-row">
            {vlanBadge && (
              <span className={`np-port-mini ${vlanBadge.mode === 'T' ? 'np-port-mini-t' : 'np-port-mini-u'}`}>
                {vlanBadge.mode} {vlanBadge.vid}
              </span>
            )}
            {!vlanBadge && l.is_fiber && <span className="np-port-mini np-port-mini-sfp">{l.media}</span>}
            {!vlanBadge && l.poe?.enabled && (
              <span className="np-port-mini np-port-mini-poe">
                ⚡{l.poe.watts != null ? `${l.poe.watts.toFixed(0)}W` : 'PoE'}
              </span>
            )}
            {stpBlock && <span className="np-port-mini np-port-mini-stp">BLK</span>}
            {hasDomWarn && <span className="np-port-mini np-port-mini-dom">DOM</span>}
            {portAnoms.length > 0 && <span className="np-port-mini np-port-mini-anom">⚠</span>}
          </span>
        </button>
      </Tooltip>
    );
  };

  // A LAG member's topology edge names the LAG, so look the peer up by the LAG's name.
  const selLag = selected ? lagGroups.find((g) => g.member_links.includes(selected.if_name)) : undefined;
  const selPeer = selected
    ? findPeerForLink(topology, devicesById, device.id, selected.if_name, selLag?.name ?? selected.lag_group_id)
    : null;
  const selAnoms: VLANFlowFinding[] = selected
    ? findingsForPort(findings, device.id, selected.if_name, selected.lag_group_id)
    : [];

  return (
    <Card withBorder radius="md" p={0}>
      <Group
        justify="space-between"
        px="md"
        py="sm"
        wrap="wrap"
        gap="xs"
        style={{ borderBottom: '1px solid var(--np-border-subtle)' }}
      >
        <Group gap="xs" wrap="wrap">
          <Text fw={700} size="sm">Port Faceplate</Text>
          <Text size="xs" c="dimmed">Click any port to inspect VLANs, peer link, counters, and DOM</Text>
        </Group>
        {deviceVlans.length > 0 && (
          <Group gap={6} wrap="wrap">
            <Text size="xs" c="dimmed">Highlight VLAN:</Text>
            <Button
              size="compact-xs"
              variant={hlVlan === null ? 'light' : 'default'}
              onClick={() => setHlVlan(null)}
            >
              None
            </Button>
            {deviceVlans.map((vid) => (
              <Button
                key={vid}
                size="compact-xs"
                variant={hlVlan === vid ? 'light' : 'default'}
                className="np-mono"
                onClick={() => setHlVlan(hlVlan === vid ? null : vid)}
                leftSection={
                  <span
                    className="np-vlan-swatch"
                    style={{ background: getVlanColor(vid, vlanColors, isDark) }}
                  />
                }
              >
                {vid}
              </Button>
            ))}
          </Group>
        )}
      </Group>

      <Stack gap="md" p="md">
        <div className="np-faceplate-bezel">
          <div className="np-faceplate-top-meta">
            <span>{device.name.toUpperCase()}{device.model ? ` · ${device.model}` : ''}</span>
            <span>{upCount}/{sortedLinks.length} UP · CLICK PORT TO INSPECT</span>
          </div>
          <div className="np-faceplate-ports-strip">
            {copperLinks.length > 0 && (
              <div className="np-faceplate-bank">{copperLinks.map(renderTile)}</div>
            )}
            {copperLinks.length > 0 && (standaloneFiber.length > 0 || lagGroups.length > 0) && (
              <div className="np-faceplate-divider" />
            )}
            {standaloneFiber.length > 0 && (
              <div className="np-faceplate-bank">{standaloneFiber.map(renderTile)}</div>
            )}
            {lagGroups.map((g) => (
              <div key={g.id} className="np-lag-bracket">
                <span className="np-lag-bracket-label">
                  LAG {g.name} · {formatSpeed(g.speed_mbps)} {g.mode.toUpperCase()}
                </span>
                <div className="np-lag-bracket-ports">
                  {g.member_links.map((mn) => {
                    const ml = sortedLinks.find((l) => l.if_name === mn);
                    return ml ? renderTile(ml) : null;
                  })}
                </div>
              </div>
            ))}
          </div>
        </div>

        {selected && (
          <Paper withBorder radius="md" p={0} style={{ background: 'var(--np-bg-subtle)' }}>
            <Group
              justify="space-between"
              px="md"
              py="xs"
              wrap="wrap"
              gap="xs"
              style={{
                background: 'var(--np-bg-surface)',
                borderBottom: '1px solid var(--np-border-subtle)',
                borderTopLeftRadius: 'var(--mantine-radius-md)',
                borderTopRightRadius: 'var(--mantine-radius-md)',
              }}
            >
              <Group gap="xs" wrap="wrap">
                <Text fw={700} size="sm" ff="monospace">{selected.if_name}</Text>
                {selected.label && <Text size="sm" c="dimmed">({selected.label})</Text>}
                <Badge size="xs" color={selected.link_up ? 'green' : 'gray'} variant="light">
                  {selected.oper_state}
                </Badge>
                <Badge size="xs" variant="outline" color="gray" ff="monospace">{selected.media}</Badge>
                {selected.is_fiber && <Badge size="xs" color="cyan" variant="light">Fiber / SFP Guard</Badge>}
                {selected.lag_group_id && (
                  <Badge size="xs" color="blue" variant="light" ff="monospace">
                    LAG: {selected.lag_group_id}
                  </Badge>
                )}
                {selected.stp_state && (
                  <Badge size="xs" color={isStpBlocked(selected.stp_state) ? 'orange' : 'teal'} variant="light">
                    STP: {selected.stp_state}{selected.stp_role ? ` (${selected.stp_role})` : ''}
                  </Badge>
                )}
              </Group>
              <Text size="xs" fw={600} ff="monospace">
                {formatSpeed(selected.speed_mbps)} {selected.duplex ?? ''}
                {selected.mtu ? ` · MTU ${selected.mtu}` : ''}
              </Text>
            </Group>

            <Stack gap="sm" p="md">
              <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">
                <div>
                  <Text size="10px" fw={700} tt="uppercase" c="dimmed">Throughput (RX / TX)</Text>
                  <Text size="xs" ff="monospace" mt={2}>
                    {selected.rates
                      ? `${formatBps(selected.rates.rx_bps)} / ${formatBps(selected.rates.tx_bps)}`
                      : '—'}
                  </Text>
                </div>
                <div>
                  <Text size="10px" fw={700} tt="uppercase" c="dimmed">Connected Peer</Text>
                  <div style={{ marginTop: 2 }}>
                    {selPeer ? (
                      <Button
                        size="compact-xs"
                        variant="default"
                        onClick={() => onOpenDevice?.(selPeer.peerId, selPeer.peerLink || null)}
                      >
                        {(selPeer.peerDevice?.name ?? selPeer.peerId)}
                        {selPeer.peerLink ? `:${selPeer.peerLink}` : ''} ({selPeer.edge.evidence.toUpperCase()}) →
                      </Button>
                    ) : (
                      <Text size="xs" c="dimmed" ff="monospace">
                        {selected.label || 'Access / Endpoint'}
                      </Text>
                    )}
                  </div>
                </div>
                <div>
                  <Text size="10px" fw={700} tt="uppercase" c="dimmed">EEE / Flow Control</Text>
                  <Text size="xs" ff="monospace" mt={2}>
                    EEE: {selected.eee === undefined ? '—' : selected.eee ? 'ON' : 'OFF'} · FC:{' '}
                    {selected.flow_control
                      ? `${selected.flow_control.rx ? 'RX ' : ''}${selected.flow_control.tx ? 'TX' : ''}`.trim() || 'OFF'
                      : '—'}
                  </Text>
                </div>
                <div>
                  <Text size="10px" fw={700} tt="uppercase" c="dimmed">Counters / PoE</Text>
                  <Text size="xs" ff="monospace" mt={2}>
                    {selected.counters
                      ? `Err: ${selected.counters.rx_errors + selected.counters.tx_errors} · Drop: ${selected.counters.rx_dropped + selected.counters.tx_dropped}`
                      : 'Counters: —'}
                    {selected.poe?.supported
                      ? ` · PoE ${selected.poe.enabled ? `${(selected.poe.watts ?? 0).toFixed(1)}W` : 'OFF'}${selected.poe.class ? ` (${selected.poe.class})` : ''}`
                      : ''}
                  </Text>
                </div>
              </SimpleGrid>

              <Group
                gap="xs"
                wrap="wrap"
                pt="xs"
                style={{ borderTop: '1px solid var(--np-border)' }}
              >
                <Text size="11px" fw={700} tt="uppercase" c="dimmed">VLANs:</Text>
                {selected.pvid != null ? (
                  <Group gap={6}>
                    <span className="np-tag-chip u">U</span>
                    <VlanChip vid={selected.pvid} vlansById={vlansById} colorMap={vlanColors} />
                  </Group>
                ) : (
                  <Text size="xs" c="dimmed">No PVID reported</Text>
                )}
                {selected.tagged_vlans.length > 0 && (
                  <>
                    <Text size="xs" c="dimmed">|</Text>
                    <Group gap={6} wrap="wrap">
                      <span className="np-tag-chip t">T</span>
                      {selected.tagged_vlans.map((vid) => (
                        <VlanChip key={vid} vid={vid} vlansById={vlansById} colorMap={vlanColors} />
                      ))}
                    </Group>
                  </>
                )}
              </Group>

              {selAnoms.map((f) => {
                const desc = flow && state
                  ? describeFinding(f, flow, state, devicesById)
                  : { title: f.message, consequence: f.message };
                return (
                  <div key={findingKey(f)} className={`np-callout ${f.severity}`}>
                    <Group justify="space-between" align="flex-start" wrap="wrap" gap="xs">
                      <div style={{ flex: 1, minWidth: 200 }}>
                        <Text size="xs" fw={700}>
                          {f.kind === 'leak' ? '⚠' : f.kind === 'tag_mismatch' ? '⇄' : '⊘'} {desc.title}
                        </Text>
                        <Text size="xs" mt={3}>{desc.consequence}</Text>
                      </div>
                      {onOpenVlanFlow && f.vlans[0] != null && (
                        <Button
                          size="compact-xs"
                          onClick={() => onOpenVlanFlow(f.vlans[0], findingKey(f))}
                        >
                          View in VLAN Flow →
                        </Button>
                      )}
                    </Group>
                  </div>
                );
              })}

              {selected.sfp?.present && (
                <Paper
                  withBorder
                  p="sm"
                  radius="sm"
                  style={{
                    background: 'var(--np-bg-surface)',
                    borderColor:
                      selected.sfp.alert_state === 'crit'
                        ? 'var(--np-red)'
                        : selected.sfp.alert_state === 'warn'
                          ? 'var(--np-amber-bright)'
                          : 'var(--np-border)',
                  }}
                >
                  <Group justify="space-between" mb="xs" wrap="wrap">
                    <Text size="xs" fw={700}>
                      SFP Optical DOM — {selected.sfp.vendor ?? 'Transceiver'} {selected.sfp.part_number ?? ''}
                      {selected.sfp.wavelength_nm != null ? ` (${selected.sfp.wavelength_nm} nm)` : ''}
                    </Text>
                    <Badge
                      size="xs"
                      color={
                        selected.sfp.alert_state === 'ok'
                          ? 'green'
                          : selected.sfp.alert_state === 'warn'
                            ? 'yellow'
                            : selected.sfp.alert_state === 'crit'
                              ? 'red'
                              : 'gray'
                      }
                    >
                      {selected.sfp.alert_state.toUpperCase()}
                    </Badge>
                  </Group>
                  <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="xs">
                    <div>
                      <Text size="10px" fw={700} tt="uppercase" c="dimmed">RX Optical Power</Text>
                      <Text
                        size="xs"
                        ff="monospace"
                        fw={selected.sfp.alert_state !== 'ok' ? 700 : 500}
                        c={selected.sfp.alert_state === 'crit' ? 'red' : selected.sfp.alert_state === 'warn' ? 'orange' : undefined}
                      >
                        {selected.sfp.rx_power_dbm != null ? `${selected.sfp.rx_power_dbm.toFixed(2)} dBm` : '—'}
                      </Text>
                    </div>
                    <div>
                      <Text size="10px" fw={700} tt="uppercase" c="dimmed">TX Optical Power</Text>
                      <Text size="xs" ff="monospace">
                        {selected.sfp.tx_power_dbm != null ? `${selected.sfp.tx_power_dbm.toFixed(2)} dBm` : '—'}
                      </Text>
                    </div>
                    <div>
                      <Text size="10px" fw={700} tt="uppercase" c="dimmed">Module Temp</Text>
                      <Text size="xs" ff="monospace">
                        {selected.sfp.temp_c != null ? `${selected.sfp.temp_c.toFixed(1)} °C` : '—'}
                      </Text>
                    </div>
                    <div>
                      <Text size="10px" fw={700} tt="uppercase" c="dimmed">Vcc / Bias</Text>
                      <Text size="xs" ff="monospace">
                        {selected.sfp.voltage_v != null ? `${selected.sfp.voltage_v.toFixed(2)} V` : '—'}
                        {selected.sfp.bias_ma != null ? ` · ${selected.sfp.bias_ma.toFixed(1)} mA` : ''}
                      </Text>
                    </div>
                  </SimpleGrid>
                  {selected.sfp.rx_power_dbm != null && (
                    <Stack gap={4} mt="xs">
                      <Group justify="space-between">
                        <Text size="10px" c="dimmed" ff="monospace">RX Power Range (-30 dBm → 0 dBm · warn &lt; -20 dBm)</Text>
                        <Text size="10px" c="dimmed" ff="monospace">{selected.sfp.rx_power_dbm.toFixed(2)} dBm</Text>
                      </Group>
                      <Progress
                        size="sm"
                        value={Math.max(0, Math.min(100, ((selected.sfp.rx_power_dbm + 30) / 30) * 100))}
                        color={
                          selected.sfp.alert_state === 'crit'
                            ? 'red'
                            : selected.sfp.alert_state === 'warn'
                              ? 'yellow'
                              : 'teal'
                        }
                      />
                    </Stack>
                  )}
                </Paper>
              )}
            </Stack>
          </Paper>
        )}
      </Stack>
    </Card>
  );
}
