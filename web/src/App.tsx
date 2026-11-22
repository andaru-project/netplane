// SPDX-License-Identifier: AGPL-3.0-or-later
import {
  AppShell,
  Badge,
  Burger,
  Button,
  Card,
  Code,
  Group,
  NavLink,
  Paper,
  Select,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  api,
  Client,
  Device,
  DeviceDetail,
  DiagnosticResult,
  AUTH_EVENT,
  formatUptime,
  NetplaneEvent,
  TopologyGraph,
} from './api';
import { PortFaceplate } from './components/PortFaceplate';
import { TopologyView } from './components/TopologyView';

type Tab = 'devices' | 'topology' | 'clients' | 'diagnostics';

export function App() {
  const [tab, setTab] = useState<Tab>('devices');
  // Bumped to reopen the event stream after the browser gives up (e.g. a 401 before the token prompt).
  const [sseEpoch, setSseEpoch] = useState(0);
  const sseFailures = useRef(0);
  const [devices, setDevices] = useState<Device[]>([]);
  const [selectedDevice, setSelectedDevice] = useState<DeviceDetail | null>(null);
  const [topology, setTopology] = useState<TopologyGraph | null>(null);
  const [clients, setClients] = useState<Client[]>([]);
  const [clientQuery, setClientQuery] = useState('');
  const [diagRuns, setDiagRuns] = useState<DiagnosticResult[]>([]);
  const [diagKind, setDiagKind] = useState<string>('fdb_lookup');
  const [diagDeviceId, setDiagDeviceId] = useState<string>('');
  const [diagTarget, setDiagTarget] = useState('');
  const [diagBusy, setDiagBusy] = useState(false);
  const [events, setEvents] = useState<NetplaneEvent[]>([]);
  const [sseConnected, setSseConnected] = useState(false);
  const [errMsg, setErrMsg] = useState<string | null>(null);
  const [navOpened, { toggle: toggleNav, close: closeNav }] = useDisclosure();
  // Async responses apply only while still current, so a slow older request never overwrites newer state.
  const refreshGen = useRef(0);
  const selectedId = useRef<string | null>(null);

  const refreshAll = useCallback(async () => {
    const gen = ++refreshGen.current;
    const id = selectedId.current;
    try {
      const [devs, topo, cls, diags, detail] = await Promise.all([
        api.listDevices(),
        api.getTopology(),
        api.listClients(clientQuery, false),
        api.listDiagnostics(25),
        id ? api.getDevice(id) : null,
      ]);
      if (gen !== refreshGen.current) return;
      setDevices(devs);
      setTopology(topo);
      setClients(cls);
      setDiagRuns(diags);
      if (detail && selectedId.current === id) setSelectedDevice(detail);
      setErrMsg(null);
    } catch (e) {
      if (gen !== refreshGen.current) return;
      setErrMsg(e instanceof Error ? e.message : String(e));
    }
  }, [clientQuery]);

  useEffect(() => {
    void refreshAll();
    const timer = window.setInterval(() => void refreshAll(), 15000);
    return () => window.clearInterval(timer);
  }, [refreshAll]);

  // The stream reads refreshAll through a ref so a new client query does not reopen it.
  const refreshRef = useRef(refreshAll);
  refreshRef.current = refreshAll;

  useEffect(() => {
    const onAuth = () => {
      sseFailures.current = 0;
      setSseEpoch((n) => n + 1);
    };
    window.addEventListener(AUTH_EVENT, onAuth);
    return () => window.removeEventListener(AUTH_EVENT, onAuth);
  }, []);

  useEffect(() => {
    const es = new EventSource('./api/v1/events/stream');
    es.onopen = () => {
      sseFailures.current = 0;
      setSseConnected(true);
    };
    let retry: number | undefined;
    es.onerror = () => {
      setSseConnected(false);
      if (es.readyState === EventSource.CLOSED) {
        // 5s, 10s, 20s, 40s, then every 60s, so a permanent 401 does not hammer the server.
        const delay = Math.min(60000, 5000 * 2 ** sseFailures.current++);
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
        // ignore non-JSON frames
      }
    };
    for (const t of [
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
    ]) {
      es.addEventListener(t, onEvent as EventListener);
    }
    return () => {
      window.clearTimeout(retry);
      es.close();
    };
  }, [sseEpoch]);

  const openDevice = async (id: string) => {
    selectedId.current = id;
    try {
      const detail = await api.getDevice(id);
      if (selectedId.current !== id) return;
      setSelectedDevice(detail);
      setTab('devices');
    } catch (e) {
      if (selectedId.current !== id) return;
      setErrMsg(e instanceof Error ? e.message : String(e));
    }
  };

  const closeDevice = () => {
    selectedId.current = null;
    setSelectedDevice(null);
  };

  const navigate = (t: Tab) => {
    closeDevice();
    setTab(t);
    closeNav();
  };

  const runDiag = async () => {
    if (!diagTarget.trim()) return;
    setDiagBusy(true);
    try {
      const res = await api.runDiagnostic({
        kind: diagKind,
        device_id: diagKind === 'fdb_lookup' ? undefined : diagDeviceId || undefined,
        target: diagTarget.trim(),
      });
      refreshGen.current++; // a refresh started before this run would drop its result
      setDiagRuns((prev) => [res, ...prev]);
      setErrMsg(null);
    } catch (e) {
      setErrMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setDiagBusy(false);
    }
  };

  const onlineDevices = devices.filter((d) => d.online).length;

  return (
    <AppShell header={{ height: 54 }} navbar={{ width: 210, breakpoint: 'sm', collapsed: { mobile: !navOpened } }} padding="md">
      <AppShell.Header px="md" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Group gap="sm">
          <Burger opened={navOpened} onClick={toggleNav} hiddenFrom="sm" size="sm" aria-label="Toggle navigation" />
          <Title order={4}>netplane</Title>
          <Badge variant="light" color="blue">Control · Info · Diagnostics</Badge>
        </Group>
        <Group gap="xs">
          <Badge color={sseConnected ? 'green' : 'gray'} variant="dot">
            {sseConnected ? 'Live SSE' : 'Connecting'}
          </Badge>
          <Button size="xs" variant="light" onClick={() => void refreshAll()}>
            Refresh
          </Button>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="xs">
        <NavLink label="Devices" active={tab === 'devices'} onClick={() => navigate('devices')} />
        <NavLink label="Topology" active={tab === 'topology'} onClick={() => navigate('topology')} />
        <NavLink label="Clients" active={tab === 'clients'} onClick={() => navigate('clients')} />
        <NavLink label="Diagnostics & Events" active={tab === 'diagnostics'} onClick={() => navigate('diagnostics')} />
      </AppShell.Navbar>

      <AppShell.Main>
        <Stack gap="md">
          {errMsg && (
            <Paper withBorder p="sm" radius="md" style={{ borderColor: '#ef4444' }}>
              <Text c="red" size="sm">{errMsg}</Text>
            </Paper>
          )}

          <SimpleGrid cols={{ base: 2, sm: 4 }}>
            <Card withBorder p="sm" radius="md">
              <Text size="xs" c="dimmed">Devices Online</Text>
              <Title order={3}>{onlineDevices} / {devices.length}</Title>
            </Card>
            <Card withBorder p="sm" radius="md">
              <Text size="xs" c="dimmed">Topology Links</Text>
              <Title order={3}>{topology?.edges.length ?? 0}</Title>
            </Card>
            <Card withBorder p="sm" radius="md">
              <Text size="xs" c="dimmed">Active Clients</Text>
              <Title order={3}>{clients.filter((c) => c.online).length}</Title>
            </Card>
            <Card withBorder p="sm" radius="md">
              <Text size="xs" c="dimmed">Recent Events</Text>
              <Title order={3}>{events.length}</Title>
            </Card>
          </SimpleGrid>

          {tab === 'devices' && !selectedDevice && (
            <Card withBorder radius="md" p="md">
              <Title order={5} mb="sm">Network Devices</Title>
              <Table striped highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Name</Table.Th>
                    <Table.Th>Driver</Table.Th>
                    <Table.Th>Role</Table.Th>
                    <Table.Th>Model</Table.Th>
                    <Table.Th>Mgmt IP</Table.Th>
                    <Table.Th>Firmware</Table.Th>
                    <Table.Th>Uptime</Table.Th>
                    <Table.Th>Status</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {devices.map((d) => (
                    <Table.Tr key={d.id} style={{ cursor: 'pointer' }} onClick={() => void openDevice(d.id)}>
                      <Table.Td>
                        <UnstyledButton
                          fw={600}
                          onClick={(e) => {
                            e.stopPropagation(); // the row handler would open it a second time
                            void openDevice(d.id);
                          }}
                        >
                          {d.name}
                        </UnstyledButton>
                      </Table.Td>
                      <Table.Td><Badge size="xs" variant="light">{d.driver}</Badge></Table.Td>
                      <Table.Td>{d.role}</Table.Td>
                      <Table.Td>{d.model || '—'}</Table.Td>
                      <Table.Td ff="monospace">{d.mgmt_ip || '—'}</Table.Td>
                      <Table.Td ff="monospace">{d.firmware.current || '—'}</Table.Td>
                      <Table.Td>{formatUptime(d.uptime_sec)}</Table.Td>
                      <Table.Td><Badge color={d.online ? 'green' : 'red'}>{d.online ? 'Online' : 'Offline'}</Badge></Table.Td>
                    </Table.Tr>
                  ))}
                  {devices.length === 0 && (
                    <Table.Tr>
                      <Table.Td colSpan={8}>
                        <Text c="dimmed" size="sm">No devices polled yet.</Text>
                      </Table.Td>
                    </Table.Tr>
                  )}
                </Table.Tbody>
              </Table>
            </Card>
          )}

          {tab === 'devices' && selectedDevice && (
            <Stack gap="md">
              <Group justify="space-between">
                <Group gap="xs">
                  <Button size="xs" variant="subtle" onClick={closeDevice}>← All Devices</Button>
                  <Title order={4}>{selectedDevice.device.name}</Title>
                  <Badge>{selectedDevice.device.driver}</Badge>
                  <Badge variant="outline">{selectedDevice.device.model || selectedDevice.device.role}</Badge>
                </Group>
                <Text size="sm" ff="monospace" c="dimmed">
                  {selectedDevice.device.mgmt_ip} · {selectedDevice.device.primary_mac}
                </Text>
              </Group>

              <PortFaceplate links={selectedDevice.links} />

              {selectedDevice.segments.length > 0 && (
                <Card withBorder radius="md" p="md">
                  <Title order={5} mb="xs">L3 Segments / Interfaces</Title>
                  <Table>
                    <Table.Thead>
                      <Table.Tr><Table.Th>Name</Table.Th><Table.Th>Kind</Table.Th><Table.Th>Addresses</Table.Th><Table.Th>State</Table.Th></Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {selectedDevice.segments.map((s) => (
                        <Table.Tr key={s.id}>
                          <Table.Td fw={600}>{s.name}</Table.Td>
                          <Table.Td>{s.kind}</Table.Td>
                          <Table.Td ff="monospace">{s.addresses.map((a) => `${a.address}/${a.prefix_length}`).join(', ') || '—'}</Table.Td>
                          <Table.Td><Badge size="xs" color={s.oper_state === 'up' ? 'green' : 'gray'}>{s.oper_state}</Badge></Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Card>
              )}

              {(selectedDevice.gateways.length > 0 || selectedDevice.vpns.length > 0 || selectedDevice.firewall) && (
                <SimpleGrid cols={{ base: 1, md: 2 }}>
                  {selectedDevice.gateways.length > 0 && (
                    <Card withBorder radius="md" p="md">
                      <Title order={5} mb="xs">Gateways</Title>
                      {selectedDevice.gateways.map((g) => (
                        <Group key={g.name} justify="space-between" mb={4}>
                          <Text size="sm" fw={600}>{g.name} ({g.gateway_ip})</Text>
                          <Badge color={g.online ? 'green' : 'red'}>
                            {g.online ? `${g.rtt_ms?.toFixed(1) ?? '0'} ms · ${g.loss_pct?.toFixed(1) ?? '0'}% loss` : 'OFFLINE'}
                          </Badge>
                        </Group>
                      ))}
                    </Card>
                  )}
                  {selectedDevice.firewall && (
                    <Card withBorder radius="md" p="md">
                      <Title order={5} mb="xs">Firewall ({selectedDevice.firewall.engine})</Title>
                      <Text size="sm" mb="xs">
                        States: {selectedDevice.firewall.states_used ?? '—'} / {selectedDevice.firewall.states_max ?? '—'}
                        {selectedDevice.firewall.saved_version ? ` · Saved v${selectedDevice.firewall.saved_version}` : ''}
                      </Text>
                      <Group gap="xs">
                        {selectedDevice.firewall.services.map((sv) => (
                          <Badge key={sv.name} size="sm" color={sv.state === 'running' ? 'green' : 'red'} variant="light">
                            {sv.name}: {sv.state}
                          </Badge>
                        ))}
                      </Group>
                    </Card>
                  )}
                </SimpleGrid>
              )}
            </Stack>
          )}

          {tab === 'topology' && <TopologyView graph={topology} onSelectDevice={(id) => void openDevice(id)} />}

          {tab === 'clients' && (
            <Card withBorder radius="md" p="md">
              <Group justify="space-between" mb="sm">
                <Title order={5}>Observed Clients</Title>
                <TextInput
                  placeholder="Search MAC, IP, hostname, SSID..."
                  value={clientQuery}
                  onChange={(e) => setClientQuery(e.currentTarget.value)}
                  size="xs"
                  w={280}
                />
              </Group>
              <Table striped>
                <Table.Thead>
                  <Table.Tr><Table.Th>Hostname</Table.Th><Table.Th>MAC</Table.Th><Table.Th>IPs</Table.Th><Table.Th>Type</Table.Th><Table.Th>Attachment</Table.Th></Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {clients.map((c) => (
                    <Table.Tr key={c.mac}>
                      <Table.Td fw={600}>{c.hostname || '—'}</Table.Td>
                      <Table.Td ff="monospace">{c.mac}</Table.Td>
                      <Table.Td ff="monospace">{c.ips.join(', ') || '—'}</Table.Td>
                      <Table.Td><Badge size="xs" variant="light">{c.connection_type}</Badge></Table.Td>
                      <Table.Td ff="monospace">{c.attached_device_id ? `${c.attached_device_id} (${c.ssid || c.attached_link || ''})` : '—'}</Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Card>
          )}

          {tab === 'diagnostics' && (
            <SimpleGrid cols={{ base: 1, md: 2 }}>
              <Card withBorder radius="md" p="md">
                <Title order={5} mb="sm">Run Diagnostic</Title>
                <Stack gap="xs">
                  <Select
                    label="Type"
                    value={diagKind}
                    onChange={(v) => {
                      setDiagKind(v ?? 'fdb_lookup');
                      setDiagDeviceId(''); // the new kind may hide the selected device
                    }}
                    data={[
                      { value: 'fdb_lookup', label: 'FDB / MAC / IP Lookup (Cross-Vendor)' },
                      { value: 'ping', label: 'Device Ping' },
                      { value: 'traceroute', label: 'Device Traceroute' },
                    ]}
                  />
                  {diagKind !== 'fdb_lookup' && (
                    <Select
                      label="Device"
                      value={diagDeviceId}
                      onChange={(v) => setDiagDeviceId(v ?? '')}
                      data={devices
                        .filter((d) => d.capabilities.includes(`cap:diag_${diagKind}`))
                        .map((d) => ({ value: d.id, label: `${d.name} (${d.driver})` }))}
                    />
                  )}
                  <TextInput
                    label={diagKind === 'fdb_lookup' ? 'Target (MAC, IP, or hostname)' : 'Target (IP or hostname)'}
                    value={diagTarget}
                    onChange={(e) => setDiagTarget(e.currentTarget.value)}
                    placeholder="e.g. de:ad:be:ef:00:01 or 192.0.2.55"
                  />
                  <Button loading={diagBusy} onClick={() => void runDiag()}>Run</Button>
                </Stack>
                <Stack gap="xs" mt="md">
                  {diagRuns.map((r) => (
                    <Paper key={r.id} withBorder p="xs" radius="sm">
                      <Group justify="space-between">
                        <Text size="xs" fw={700} ff="monospace">{r.kind} · {r.target}</Text>
                        <Badge size="xs" color={r.ok ? 'green' : 'red'}>{r.ok ? 'OK' : 'FAIL'}</Badge>
                      </Group>
                      <Text size="xs" mt={4}>{r.summary}</Text>
                      {r.raw_output && <Code block mt={4} fz="xs">{r.raw_output}</Code>}
                    </Paper>
                  ))}
                </Stack>
              </Card>

              <Card withBorder radius="md" p="md">
                <Title order={5} mb="sm">Live Event Stream (SSE)</Title>
                <Stack gap="xs">
                  {events.map((ev) => (
                    <Paper key={ev.id} withBorder p="xs" radius="sm">
                      <Group justify="space-between">
                        <Badge size="xs" color={ev.severity === 'crit' ? 'red' : ev.severity === 'warn' ? 'yellow' : 'blue'}>
                          {ev.type}
                        </Badge>
                        <Text size="xs" c="dimmed">{new Date(ev.timestamp).toLocaleTimeString()}</Text>
                      </Group>
                      <Text size="xs" mt={4}>{ev.summary}</Text>
                    </Paper>
                  ))}
                  {events.length === 0 && <Text c="dimmed" size="sm">Waiting for live network transitions...</Text>}
                </Stack>
              </Card>
            </SimpleGrid>
          )}
        </Stack>
      </AppShell.Main>
    </AppShell>
  );
}
