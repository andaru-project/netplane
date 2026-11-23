// SPDX-License-Identifier: AGPL-3.0-or-later
import { createTheme, MantineProvider } from '@mantine/core';
import { render, screen } from '@testing-library/react';
import { BrowserRouter } from 'react-router';
import { afterEach, describe, expect, it } from 'vitest';
import { apiUrl } from './api';
import { apiBasePath, getRouterBasename } from './routing';
import { Shell } from './Shell';

const testTheme = createTheme({ primaryColor: 'teal', defaultRadius: 'sm' });

function setBaseHref(href: string | null) {
  document.head.querySelectorAll('base').forEach((el) => el.remove());
  if (href !== null) {
    const base = document.createElement('base');
    base.setAttribute('href', href);
    document.head.appendChild(base);
  }
}

afterEach(() => {
  setBaseHref(null);
  window.history.replaceState({}, '', '/');
});

describe('Ingress base href routing', () => {
  it('resolves basename and API paths from Home Assistant Ingress <base href>', () => {
    setBaseHref('/api/hassio_ingress/tok_abc123/');
    window.history.replaceState({}, '', '/api/hassio_ingress/tok_abc123/vlans/flow');

    expect(getRouterBasename()).toBe('/api/hassio_ingress/tok_abc123');
    expect(apiBasePath()).toBe('/api/hassio_ingress/tok_abc123/');
    expect(apiUrl('/vlans/flow')).toBe('/api/hassio_ingress/tok_abc123/api/v1/vlans/flow');
  });

  it('keeps basename at "/" for standalone <base href="./"> even on nested routes', () => {
    setBaseHref('./');
    window.history.replaceState({}, '', '/devices/sw-core-rack');

    expect(getRouterBasename()).toBe('/');
    expect(apiBasePath()).toBe('/');
    expect(apiUrl('/devices')).toBe('/api/v1/devices');
  });

  it('routes screens and prefixes every navigation link under an Ingress base href', () => {
    const prefix = '/api/hassio_ingress/ha_token_99';
    setBaseHref(`${prefix}/`);
    window.history.replaceState({}, '', `${prefix}/vlans/flow`);

    const basename = getRouterBasename();
    expect(basename).toBe(prefix);

    render(
      <MantineProvider theme={testTheme}>
        <BrowserRouter basename={basename}>
          <Shell
            state={{
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
            }}
            topology={{ generated_at: '2026-01-01T00:00:00Z', root_ids: [], nodes: [], edges: [] }}
            flow={{ generated_at: '2026-01-01T00:00:00Z', vlans: [], findings: [] }}
            targets={[]}
            diagRuns={[]}
            events={[]}
            sseConnected={true}
            errMsg={null}
            pollBusy={false}
            refreshNonce={0}
            onPollAll={() => {}}
            onDiagnosticCompleted={() => {}}
          />
        </BrowserRouter>
      </MantineProvider>,
    );

    // Renders the VLAN Flow screen matched under the Ingress prefix.
    expect(screen.getByRole('heading', { name: 'VLAN Flow' })).toBeInTheDocument();

    // Sidebar links stay scoped to the Ingress basename.
    const topoLink = screen.getByRole('link', { name: 'Topology' });
    expect(topoLink).toHaveAttribute('href', `${prefix}/topology`);

    const devicesLink = screen.getByRole('link', { name: 'Devices' });
    expect(devicesLink).toHaveAttribute('href', `${prefix}/devices`);

    const diagLink = screen.getByRole('link', { name: 'Diagnostics & Events' });
    expect(diagLink).toHaveAttribute('href', `${prefix}/diagnostics`);
  });
});
