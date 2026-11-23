// SPDX-License-Identifier: AGPL-3.0-or-later
import { createTheme, MantineProvider } from '@mantine/core';
import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import type { NetworkState } from './api';
import { OverviewScreen } from './screens/OverviewScreen';

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
