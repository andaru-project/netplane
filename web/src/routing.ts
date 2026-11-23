// SPDX-License-Identifier: AGPL-3.0-or-later

/**
 * Resolves the React Router basename from the document's <base href> / document.baseURI.
 *
 * Behind Home Assistant Ingress, the Go server injects `<base href="/api/hassio_ingress/<token>/">`,
 * producing basename `"/api/hassio_ingress/<token>"`.
 * In standalone mode, the Go server injects `<base href="./">`, where `document.baseURI` changes
 * with nested URLs (e.g. `/devices/sw-1` -> `/devices/`), so a relative `./` base always maps to `"/"`.
 */
export function getRouterBasename(
  doc: Pick<Document, 'baseURI' | 'querySelector'> = document,
  loc: Pick<Location, 'href'> | undefined = typeof window !== 'undefined' ? window.location : undefined,
): string {
  const baseEl = doc.querySelector('base');
  if (baseEl) {
    const rawHref = baseEl.getAttribute('href')?.trim() ?? '';
    if (rawHref === '' || rawHref === './' || rawHref === '.') {
      return '/';
    }
    try {
      const pathname = new URL(doc.baseURI).pathname.replace(/\/+$/, '');
      return pathname || '/';
    } catch {
      return '/';
    }
  }

  if (doc.baseURI && loc?.href && doc.baseURI !== loc.href) {
    try {
      const pathname = new URL(doc.baseURI).pathname.replace(/\/+$/, '');
      return pathname || '/';
    } catch {
      return '/';
    }
  }

  return '/';
}

/**
 * Returns the app's URL prefix with a trailing slash (`"/"` or `"/api/hassio_ingress/<token>/"`)
 * for API fetches, EventSource streams, and auth cookie scoping across nested routes.
 */
export function apiBasePath(
  doc: Pick<Document, 'baseURI' | 'querySelector'> = document,
  loc: Pick<Location, 'href'> | undefined = typeof window !== 'undefined' ? window.location : undefined,
): string {
  const base = getRouterBasename(doc, loc);
  return base === '/' ? '/' : `${base}/`;
}

export function devicePath(deviceId: string, portName?: string | null): string {
  const base = `/devices/${encodeURIComponent(deviceId)}`;
  if (portName) {
    return `${base}?port=${encodeURIComponent(portName)}`;
  }
  return base;
}

export function vlanFlowPath(opts?: {
  vlanId?: number | null;
  findingKey?: string | null;
  mode?: 'single' | 'all';
}): string {
  const params = new URLSearchParams();
  if (opts?.mode) params.set('mode', opts.mode);
  if (opts?.vlanId != null) params.set('vlan', String(opts.vlanId));
  if (opts?.findingKey) params.set('finding', opts.findingKey);
  const qs = params.toString();
  return qs ? `/vlans/flow?${qs}` : '/vlans/flow';
}
