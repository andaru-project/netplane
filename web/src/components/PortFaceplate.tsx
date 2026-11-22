// SPDX-License-Identifier: AGPL-3.0-or-later
import { Badge, Card, Group, Paper, SimpleGrid, Stack, Text, Tooltip } from '@mantine/core';
import { useState } from 'react';
import { formatBps, formatSpeed, Link } from '../api';

interface Props {
  links: Link[];
}

export function PortFaceplate({ links }: Props) {
  const [selectedIfName, setSelectedIfName] = useState<string | null>(links[0]?.if_name ?? null);
  const selected = links.find((l) => l.if_name === selectedIfName) ?? links[0] ?? null;

  if (links.length === 0) {
    return <Text c="dimmed" size="sm">No physical links reported.</Text>;
  }

  return (
    <Stack gap="md">
      <Paper withBorder p="md" radius="md" bg="dark.8">
        <Group gap="xs" wrap="wrap">
          {links.map((l) => {
            const isSel = selected?.if_name === l.if_name;
            const statusColor = !l.admin_up
              ? '#6b7280'
              : l.stp_state === 'blocking' || l.stp_state === 'discarding'
                ? '#f59e0b'
                : l.link_up
                  ? l.is_fiber
                    ? '#38bdf8'
                    : '#22c55e'
                  : '#374151';

            return (
              <Tooltip
                key={l.if_name}
                label={`${l.if_name} (${l.media}) ${l.link_up ? formatSpeed(l.speed_mbps) : l.oper_state}`}
              >
                <Paper
                  onClick={() => setSelectedIfName(l.if_name)}
                  p="xs"
                  radius="sm"
                  style={{
                    cursor: 'pointer',
                    minWidth: 68,
                    background: l.is_fiber ? '#172554' : '#1f2937',
                    border: isSel ? '2px solid #fbbf24' : `2px solid ${statusColor}`,
                    textAlign: 'center',
                  }}
                >
                  <Text size="xs" fw={700} c="white" ff="monospace">
                    {l.if_name}
                  </Text>
                  <Text size="10px" c={l.link_up ? 'green.3' : 'dimmed'} fw={600}>
                    {l.link_up ? formatSpeed(l.speed_mbps) : 'DOWN'}
                  </Text>
                  <Group gap={3} justify="center" mt={4}>
                    {l.is_fiber && (
                      <Badge size="xs" variant="filled" color="cyan" px={4}>
                        {l.media}
                      </Badge>
                    )}
                    {l.poe?.enabled && (
                      <Badge size="xs" variant="filled" color="yellow" px={4}>
                        PoE
                      </Badge>
                    )}
                    {l.sfp?.alert_state && l.sfp.alert_state !== 'ok' && (
                      <Badge size="xs" variant="filled" color="red" px={4}>
                        DOM
                      </Badge>
                    )}
                  </Group>
                </Paper>
              </Tooltip>
            );
          })}
        </Group>
      </Paper>

      {selected && (
        <Card withBorder radius="md" p="md">
          <Group justify="space-between" mb="xs">
            <Group gap="xs">
              <Text fw={700} ff="monospace">
                {selected.if_name}
              </Text>
              {selected.label && (
                <Text c="dimmed" size="sm">
                  ({selected.label})
                </Text>
              )}
              <Badge color={selected.link_up ? 'green' : 'gray'}>{selected.oper_state}</Badge>
              <Badge variant="outline">{selected.media}</Badge>
              {selected.is_fiber && <Badge color="cyan">Fiber / SFP Guard</Badge>}
              {selected.stp_state && (
                <Badge color={selected.stp_state === 'forwarding' ? 'teal' : 'orange'}>
                  STP: {selected.stp_state}
                </Badge>
              )}
            </Group>
            <Text size="sm" ff="monospace">
              {formatSpeed(selected.speed_mbps)} {selected.duplex ?? ''}
            </Text>
          </Group>

          <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="xs">
            <div>
              <Text size="xs" c="dimmed">Throughput</Text>
              <Text size="sm" ff="monospace">
                RX {formatBps(selected.rates?.rx_bps)} / TX {formatBps(selected.rates?.tx_bps)}
              </Text>
            </div>
            <div>
              <Text size="xs" c="dimmed">VLANs</Text>
              <Text size="sm" ff="monospace">
                PVID {selected.pvid ?? '—'}
                {selected.tagged_vlans.length > 0 ? ` | T:${selected.tagged_vlans.join(',')}` : ''}
              </Text>
            </div>
            <div>
              <Text size="xs" c="dimmed">EEE / Flow Control</Text>
              <Text size="sm">
                EEE: {selected.eee === undefined ? '—' : selected.eee ? 'ON' : 'OFF'} | FC:{' '}
                {selected.flow_control ? `${selected.flow_control.rx ? 'RX' : ''}${selected.flow_control.tx ? ' TX' : ''}`.trim() || 'OFF' : '—'}
              </Text>
            </div>
            <div>
              <Text size="xs" c="dimmed">Errors / PoE</Text>
              <Text size="sm" ff="monospace">
                Err: {(selected.counters?.rx_errors ?? 0) + (selected.counters?.tx_errors ?? 0)}
                {selected.poe?.supported ? ` | ${selected.poe.watts?.toFixed(1) ?? '0'}W` : ''}
              </Text>
            </div>
          </SimpleGrid>

          {selected.sfp?.present && (
            <Paper withBorder p="sm" mt="sm" radius="sm">
              <Group justify="space-between" mb={4}>
                <Text size="xs" fw={700}>
                  SFP Optical DOM — {selected.sfp.vendor ?? 'Unknown'} {selected.sfp.part_number ?? ''}
                </Text>
                <Badge
                  size="xs"
                  color={selected.sfp.alert_state === 'ok' ? 'green' : selected.sfp.alert_state === 'warn' ? 'yellow' : 'red'}
                >
                  {selected.sfp.alert_state.toUpperCase()}
                </Badge>
              </Group>
              <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="xs">
                <Text size="xs" ff="monospace">RX: {selected.sfp.rx_power_dbm?.toFixed(2) ?? '—'} dBm</Text>
                <Text size="xs" ff="monospace">TX: {selected.sfp.tx_power_dbm?.toFixed(2) ?? '—'} dBm</Text>
                <Text size="xs" ff="monospace">Temp: {selected.sfp.temp_c?.toFixed(1) ?? '—'} °C</Text>
                <Text size="xs" ff="monospace">Vcc: {selected.sfp.voltage_v?.toFixed(2) ?? '—'} V</Text>
              </SimpleGrid>
            </Paper>
          )}
        </Card>
      )}
    </Stack>
  );
}
