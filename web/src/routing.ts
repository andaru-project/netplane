// SPDX-License-Identifier: AGPL-3.0-or-later

/**
 * Resolves the React Router basename from the document's <base href>, which the Go server points at
 * the app's root: the Ingress prefix behind Home Assistant (`/api/hassio_ingress/<token>/`), and
 * otherwise a relative path (`./`, `../`, ...) that climbs from the current route back to the root,
 * so the app also works under a reverse-proxy sub-path.
 */
export function getRouterBasename(doc: Pick<Document, 'baseURI'> = document): string {
  try {
    return new URL(doc.baseURI).pathname.replace(/\/+$/, '') || '/';
  } catch {
    return '/';
  }
}

/**
 * Returns the app's URL prefix with a trailing slash (`"/"` or `"/api/hassio_ingress/<token>/"`)
 * for API fetches, EventSource streams, and auth cookie scoping across nested routes.
 */
let pinnedRoot: string | undefined;

/**
 * Reads the app root once, at startup, before the router navigates. The spec freezes a relative
 * base URL, but an engine that re-resolved it after pushState would otherwise follow the route.
 */
export function pinAppRoot(doc: Pick<Document, 'baseURI'> = document): string {
  pinnedRoot = getRouterBasename(doc);
  return pinnedRoot;
}

export function apiBasePath(doc: Pick<Document, 'baseURI'> = document): string {
  const base = pinnedRoot ?? getRouterBasename(doc);
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
