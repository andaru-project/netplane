// SPDX-License-Identifier: AGPL-3.0-or-later
import { Badge, Group, Paper, Stack, Text } from '@mantine/core';
import { formatSpeed, TopologyGraph, TopologyNode } from '../api';

interface Props {
  graph: TopologyGraph | null;
  onSelectDevice: (id: string) => void;
}

export function TopologyView({ graph, onSelectDevice }: Props) {
  if (!graph || graph.nodes.length === 0) {
    return (
      <Paper withBorder p="xl" radius="md">
        <Text c="dimmed">No topology nodes discovered yet. Configure targets and poll to build the cross-vendor map.</Text>
      </Paper>
    );
  }

  // Compute BFS layers from root_ids for a clean hierarchical layout.
  const adj = new Map<string, string[]>();
  for (const e of graph.edges) {
    adj.set(e.from_id, [...(adj.get(e.from_id) ?? []), e.to_id]);
  }
  const layer = new Map<string, number>();
  const roots = graph.root_ids.length > 0 ? graph.root_ids : [graph.nodes[0].id];
  const queue: string[] = [];
  for (const r of roots) {
    layer.set(r, 0);
    queue.push(r);
  }
  while (queue.length > 0) {
    const cur = queue.shift()!;
    const d = layer.get(cur) ?? 0;
    for (const nxt of adj.get(cur) ?? []) {
      if (!layer.has(nxt)) {
        layer.set(nxt, d + 1);
        queue.push(nxt);
      }
    }
  }
  for (const n of graph.nodes) {
    if (!layer.has(n.id)) layer.set(n.id, n.kind === 'client' ? 3 : 1);
  }

  const byLayer = new Map<number, TopologyNode[]>();
  for (const n of graph.nodes) {
    const lv = layer.get(n.id) ?? 0;
    byLayer.set(lv, [...(byLayer.get(lv) ?? []), n]);
  }
  const levels = Array.from(byLayer.keys()).sort((a, b) => a - b);

  const nodeW = 165;
  const nodeH = 56;
  const gapX = 28;
  const gapY = 96;
  const maxCols = Math.max(...levels.map((lv) => (byLayer.get(lv) ?? []).length), 1);
  const svgW = Math.max(760, maxCols * (nodeW + gapX) + 60);
  const svgH = Math.max(320, levels.length * (nodeH + gapY) + 60);

  const pos = new Map<string, { x: number; y: number }>();
  levels.forEach((lv, rowIdx) => {
    const nodes = byLayer.get(lv) ?? [];
    const rowW = nodes.length * nodeW + (nodes.length - 1) * gapX;
    const startX = (svgW - rowW) / 2;
    const y = 30 + rowIdx * (nodeH + gapY);
    nodes.forEach((n, colIdx) => {
      pos.set(n.id, { x: startX + colIdx * (nodeW + gapX), y });
    });
  });

  return (
    <Stack gap="sm">
      <Group justify="space-between">
        <Group gap="xs">
          <Badge variant="light" color="blue">Nodes: {graph.nodes.length}</Badge>
          <Badge variant="light" color="teal">Links: {graph.edges.length}</Badge>
        </Group>
        <Text size="xs" c="dimmed">Click any device node to inspect ports & telemetry</Text>
      </Group>

      <Paper withBorder p="sm" radius="md" style={{ overflowX: 'auto', background: '#0f172a' }}>
        <svg width={svgW} height={svgH}>
          {graph.edges.map((e) => {
            const p1 = pos.get(e.from_id);
            const p2 = pos.get(e.to_id);
            if (!p1 || !p2) return null;
            const x1 = p1.x + nodeW / 2;
            const y1 = p1.y + nodeH;
            const x2 = p2.x + nodeW / 2;
            const y2 = p2.y;
            const stroke =
              e.kind === 'lag'
                ? '#38bdf8'
                : e.kind === 'wireless_uplink' || e.kind === 'wireless_client'
                  ? '#a78bfa'
                  : '#22c55e';
            const dash = e.kind.startsWith('wireless') ? '5,4' : undefined;
            const mx = (x1 + x2) / 2;
            const my = (y1 + y2) / 2;
            const caption = [e.from_link, e.speed_mbps ? formatSpeed(e.speed_mbps) : '', e.evidence]
              .filter(Boolean)
              .join(' · ');
            return (
              <g key={e.id}>
                <line x1={x1} y1={y1} x2={x2} y2={y2} stroke={stroke} strokeWidth={e.kind === 'lag' ? 3 : 1.8} strokeDasharray={dash} />
                <text x={mx} y={my - 4} textAnchor="middle" fill="#cbd5e1" fontSize="10" fontFamily="monospace">
                  {caption}
                </text>
              </g>
            );
          })}

          {graph.nodes.map((n) => {
            const p = pos.get(n.id);
            if (!p) return null;
            const isDev = n.kind === 'device';
            const fill = isDev ? '#1e293b' : '#172033';
            const stroke = !n.online ? '#ef4444' : isDev ? '#38bdf8' : '#64748b';
            return (
              <g
                key={n.id}
                transform={`translate(${p.x},${p.y})`}
                style={{ cursor: isDev ? 'pointer' : 'default' }}
                onClick={() => isDev && onSelectDevice(n.id)}
              >
                <rect width={nodeW} height={nodeH} rx={8} fill={fill} stroke={stroke} strokeWidth={1.6} />
                <text x={10} y={22} fill="#f8fafc" fontSize="12" fontWeight="700" fontFamily="system-ui, sans-serif">
                  {n.label.slice(0, 20)}
                </text>
                <text x={10} y={40} fill="#94a3b8" fontSize="10" fontFamily="monospace">
                  {isDev ? `${n.driver ?? ''} · ${n.role ?? ''}` : n.ip || n.mac || 'client'}
                </text>
              </g>
            );
          })}
        </svg>
      </Paper>
    </Stack>
  );
}
