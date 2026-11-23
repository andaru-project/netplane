// SPDX-License-Identifier: AGPL-3.0-or-later
import { createTheme, MantineProvider } from '@mantine/core';
import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import type { Device, Link, NetworkState, VLANFlowFinding } from './api';
import { PortFaceplate } from './components/PortFaceplate';
import { FindingCallout } from './components/ui';
import { OverviewScreen } from './screens/OverviewScreen';
import { VlanFlowScreen } from './screens/VlanFlowScreen';
import { vlanFlowPath } from './routing';
import { findingKey } from './vlanUtils';

const testTheme = createTheme({ primaryColor: 'teal', defaultRadius: 'sm' });

function wrap(ui: ReactNode, path = '/') {
  return render(
    <MantineProvider theme={testTheme}>
      <MemoryRouter initialEntries={[path]}>{ui}</MemoryRouter>
    </MantineProvider>,
  );
}

const emptyState: NetworkState = {
  devices: [],
  links: [],
  lag_groups: [],
  segments: [],
  zones: [],
  gateways: [],
  routes: [],
  vpns: [],
  firewalls: [],
  radios: [],
  wireless_uplinks: [],
  clients: [],
};

describe('OverviewScreen', () => {
  it('shows VLAN health as unavailable until a flow report loads', () => {
    wrap(
      <OverviewScreen state={emptyState} topology={null} flow={null} events={[]} sseConnected devicesById={new Map()} />,
    );
    expect(screen.getByText('Unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Clean')).not.toBeInTheDocument();
    expect(screen.queryByText('All Clear')).not.toBeInTheDocument();
  });

  it('reports a loaded report without findings as clean', () => {
    wrap(
      <OverviewScreen
        state={emptyState}
        topology={null}
        flow={{ generated_at: '2026-01-01T00:00:00Z', vlans: [], findings: [] }}
        events={[]}
        sseConnected
        devicesById={new Map()}
      />,
    );
    expect(screen.getByText('Clean')).toBeInTheDocument();
    expect(screen.getByText('All Clear')).toBeInTheDocument();
  });
});

describe('FindingCallout', () => {
  const finding: VLANFlowFinding = {
    kind: 'dead_end',
    severity: 'warn',
    vlans: [],
    edge_id: 'e1',
    device_id: 'sw1',
    link: 'p1',
    peer_id: 'sw2',
    peer_link: 'p2',
    message: 'VLAN stops here',
    stranded: [],
  };

  it('runs onAction without a title, even with no VLAN to trace', () => {
    const onAction = vi.fn();
    wrap(<FindingCallout finding={finding} actionLabel="Select" onAction={onAction} />);
    fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    expect(onAction).toHaveBeenCalledOnce();
  });

  it('falls back to the trace link without onAction', () => {
    wrap(<FindingCallout finding={{ ...finding, vlans: [20] }} actionLabel="Trace" />);
    expect(screen.getByRole('link', { name: 'Trace' }).getAttribute('href')).toContain('vlan=20');
  });
});

describe('VlanFlowScreen', () => {
  const vlan = (id: number) => ({
    id,
    gateways: [],
    devices: [],
    reached: [],
    hops: [],
    access_ports: [],
    ssids: [],
    clients: 0,
  });
  const finding20: VLANFlowFinding = {
    kind: 'dead_end',
    severity: 'warn',
    vlans: [20],
    edge_id: 'e1',
    device_id: 'sw1',
    link: 'p1',
    peer_id: 'sw2',
    peer_link: 'p2',
    message: 'VLAN 20 stops here',
    stranded: [],
  };
  const flow = { generated_at: '2026-01-01T00:00:00Z', vlans: [vlan(10), vlan(20)], findings: [finding20] };
  const renderAt = (vlanId: number) =>
    wrap(
      <VlanFlowScreen
        flow={flow}
        state={null}
        topology={null}
        devicesById={new Map()}
        vlansById={new Map(flow.vlans.map((v) => [v.id, v]))}
        vlanColors={new Map()}
      />,
      vlanFlowPath({ mode: 'single', vlanId, findingKey: findingKey(finding20) }),
    );

  it('ignores a finding URL param for another VLAN in single mode', () => {
    renderAt(10);
    expect(screen.queryByText('Consequence')).not.toBeInTheDocument();
  });

  it('shows the finding when it belongs to the selected VLAN', () => {
    renderAt(20);
    expect(screen.getByText('Consequence')).toBeInTheDocument();
  });
});

describe('PortFaceplate', () => {
  const device: Device = {
    id: 'sw1',
    driver: 'openwrt',
    target_id: 't1',
    name: 'Switch',
    role: 'switch',
    macs: [],
    firmware: {},
    uptime_sec: 1,
    online: true,
    last_seen: '2026-01-01T00:00:00Z',
    capabilities: [],
    system_stats: {},
    sensors: [],
  };
  const port = (pvid: number, extra: Partial<Link> = {}): Link => ({
    device_id: 'sw1',
    index: 1,
    if_name: 'lan1',
    admin_up: true,
    link_up: true,
    oper_state: 'up',
    speed_mbps: 1000,
    media: 'copper',
    is_fiber: false,
    pvid,
    tagged_vlans: [],
    ...extra,
  });
  const faceplate = (links: Link[]) => (
    <PortFaceplate
      device={device}
      links={links}
      lagGroups={[]}
      flow={null}
      state={null}
      topology={null}
      devicesById={new Map()}
      vlansById={new Map()}
      vlanColors={new Map()}
      selectedPort={null}
      onSelectPort={() => {}}
      onOpenDevice={() => {}}
      onOpenVlanFlow={() => {}}
    />
  );

  it('clears the VLAN highlight when a refresh removes that VLAN', () => {
    const { rerender } = wrap(faceplate([port(20)]));
    fireEvent.click(screen.getByRole('button', { name: '20' }));
    expect(screen.getByRole('button', { name: '20' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'None' })).toHaveAttribute('aria-pressed', 'false');

    rerender(
      <MantineProvider theme={testTheme}>
        <MemoryRouter>{faceplate([port(10)])}</MemoryRouter>
      </MantineProvider>,
    );
    expect(screen.getByRole('button', { name: 'None' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('shows a missing PoE draw as unknown, not 0 W', () => {
    wrap(faceplate([port(10, { poe: { supported: true, enabled: true } })]));
    expect(screen.getByText(/PoE —/)).toBeInTheDocument();
    expect(screen.queryByText(/PoE 0\.0W/)).not.toBeInTheDocument();
  });
});
