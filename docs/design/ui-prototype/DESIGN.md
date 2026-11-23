# Andaru Netplane — Web UI Design Specification

**Status:** Design proposal & clickable prototype  
**Target repositories:** `andaru-project/netplane` (issues `#17`, `#18`), visual alignment with `andaru-project/andaru`  
**Stack target (production):** React + TypeScript + Vite + Mantine v9 (embedded in Go binary via `embed.FS`, zero runtime CDN dependencies, Home Assistant Ingress compatible)

---

## 1. Visual Alignment with Andaru

Netplane ships alongside Andaru (`netplane#17`) and must feel like a native sibling rather than a separate appliance UI. Every color, radius, font, and shell decision derives directly from `andaru/web/src/main.tsx`, `Shell.tsx`, `styles.css`, `andaru-icon.svg`, and `docs/research/web-ui-foundation.md`:

| Token / Rule | Andaru Source | Netplane Usage |
|---|---|---|
| **Component Kit & Theme** | `createTheme({ primaryColor: 'teal', defaultRadius: 'sm', fontFamily: 'system-ui, sans-serif' })` | Mantine `teal` primary accent (`#0ca678` light / `#20c997` dark), `4px` (`sm`) radius for controls/badges, `6px–8px` for cards/panels. |
| **Color Scheme** | `defaultColorScheme="auto"` (`light-dark(gray-0, dark-8)`) | Full Light and Dark mode parity. Replaces Netplane's current hardcoded dark SVG backgrounds (`#0f172a` in `TopologyView.tsx` and `dark.8` in `PortFaceplate.tsx`). |
| **Brand Mark Accent** | `andaru-icon.svg` (`#ffb21c` amber) | Used exclusively for the Andaru mark and high-contrast badges with dark ink `#17212c` (9.02:1 contrast). Never used as body text on white (1.80:1 fails WCAG). |
| **Typography** | System UI sans + system monospace | `system-ui, sans-serif` for labels/prose; `ui-monospace, SFMono-Regular, Menlo, monospace` for IPs, MACs, interface names (`igc0`, `sfp1`, `lan1`), VLAN IDs, speeds, and optical DOM metrics. Zero web-font fetches. |
| **Shell Chrome** | `56px` (`3.5rem`) header, `240px` (`15rem`) desktop sidebar, mobile drawer below `900px` (`56.25em`) | Identical shell geometry, uppercase dimmed nav group headers, compact header with brand mark, live connection badge (`Live SSE`), anomaly count shortcut, theme toggle, and manual poll action. |
| **Accessibility** | WCAG 2.2 Use of Color (`web-ui-foundation.md`) | State is never conveyed by color alone: every status, link kind, VLAN tag mode (`T` / `U` / `✕`), and anomaly (`⚠ LEAK` / `⊘ DEAD-END`) pairs hue with shape, stroke pattern, icon, and text label. |

---

## 2. Information Architecture & Navigation

Netplane's existing UI (`web/src/App.tsx`) has four flat tabs (`Devices`, `Topology`, `Clients`, `Diagnostics & Events`) with a global 4-card KPI bar pinned above every tab. As VLAN analysis, L3 segments/zones, and future Phase 2 change review (`Save → Preview → Apply`) enter the app, a flat tab list breaks down.

### Proposed Navigation Structure

Mirroring `andaru/web/src/Shell.tsx`, the left sidebar uses concise uppercase groups:

```text
MONITOR
  ├── Overview            (Health summary, actionable alerts, WAN/PoE/device snapshot)
  ├── Topology            (Interactive L2/L3/Wi-Fi physical & logical graph + VLAN overlay)
  └── VLAN Flow       [3] (Signature cross-device VLAN propagation & anomaly diagram)

INVENTORY
  ├── Devices             (Multi-vendor inventory table → Device Detail + Port Faceplate)
  ├── VLANs & Segments    (Unified VLAN/Segment/Zone table + cross-switch tagging matrix)
  └── Clients             (Searchable wired/wireless client table with FDB/AP attachment)

OPERATIONS  (documented for full production IA; existing Diagnostics & Phase 2 Changes)
  ├── Diagnostics & Events (Existing fdb_lookup, ping, traceroute + live SSE stream)
  └── Review & Apply       (Phase 2: matches Andaru's save/preview/rollback workflow)
```

> **Prototype scope:** The clickable static prototype implements all 5 requested screens (**Overview**, **Topology**, **VLAN Flow**, **VLANs & Segments**, **Devices → Device Detail with Port Faceplate**) plus a working **Clients** screen so every counter and cross-link resolves without dead ends.

### Cross-Screen Navigation Flows

Every screen links directly into related context rather than trapping the operator in a single view:
- **Overview → VLAN Flow:** Clicking any VLAN anomaly alert (or the header issue badge) jumps directly to `VLAN Flow` pre-filtered to that VLAN with the offending link selected in the inspector.
- **Overview / Topology / VLAN Flow / Clients → Device Detail:** Clicking any device name, topology node, or port opens that device's detail screen and (when coming from a port alert such as SFP DOM warning) pre-selects that exact port on the faceplate.
- **Device Detail (Faceplate) → VLAN Flow:** Selecting a port that participates in a VLAN leak or dead-end shows an inline warning card with a direct link to inspect its downstream consequence in `VLAN Flow`.
- **VLANs & Segments → VLAN Flow:** Clicking any VLAN row's health status or "Trace flow" action opens `VLAN Flow` scoped to that VLAN.

---

## 3. Screen-by-Screen Design

### 3.1 Overview (`/overview`)

**Purpose:** Answer *"Is the network healthy right now, and what needs my attention?"* in under five seconds without burying issues behind generic KPI tiles.

**Layout:**
1. **Page Header:** Title (`Overview`), subtitle (`Cross-vendor status across 6 devices and 5 drivers`), last-poll timestamp.
2. **Compact KPI Strip (4 columns desktop, 2×2 phone):**
   - `Devices Online` (`6 / 6`, active driver list)
   - `VLAN Health` (`3 issues`: `1 Leak · 2 Dead-ends`, clickable to VLAN Flow)
   - `Active Clients` (`22 online`: `9 wired · 13 wireless`, clickable to Clients)
   - `WAN & PoE` (`wan-fiber 4.2 ms · 0% loss`, PoE draw `31.4 / 52 W`)
3. **Attention Required (Actionable Findings List):** Ranked by severity (`CRIT` → `WARN`), styled as clean bordered list rows (not noisy full-width colored banners). Surfaces cross-VLAN leaks, VLAN dead-ends, SFP DOM optical alarms, and offline devices—each with a 1-click deep link to the exact screen/port.
4. **Two-Column Operational Summary (stacks on phone):**
   - **Left — Devices Snapshot:** Compact table of devices (name, driver badge, role, mgmt IP, ports up/total, health badge). Clicking any row opens Device Detail.
   - **Right — Gateways, Firewall & Live Events:** WAN gateway latency/loss, firewall state table bar (`nftables`), and recent SSE state transitions (`sfp.alert`, `link.stp_changed`, `topology.updated`).

### 3.2 Topology (`/topology`)

**Purpose:** Show the solved cross-vendor physical & wireless graph (`topology.Graph`)—how gateways, switches, LAGs, APs, mesh uplinks, and clients physically connect, with confidence/evidence transparency.

**Layout:**
1. **Toolbar:**
   - **VLAN Overlay selector** (`None` or any VLAN ID — inspired by Omada v6.2): dims unrelated edges and overlays pass (`green`), dead-end/pruned (`amber`), or leak (`red`) status directly on physical links.
   - **Toggles:** `Show clients` (off by default to keep infrastructure readable), `Show evidence` (`LLDP`, `LAG`, `Wireless`, confidence score).
   - **Legend:** Distinct line styles for Ethernet, LAG (twin-rail), Wireless Uplink (dashed), and STP Blocking (dotted gray).
2. **Interactive SVG Canvas:** Hierarchical top-down layout rooted at gateways/firewalls, using theme-aware CSS variables (works natively in Light and Dark mode). Distinct geometric badges per device role (firewall, switch, AP, client).
3. **Side Inspector Panel (right column on desktop, stacked below canvas on phone):** Clicking a device shows hardware summary, IP/MAC, active port count, and an `Open Device & Faceplate` button; clicking an edge shows both endpoint ports, negotiated speed, LAG members, discovery evidence/confidence, and carried VLANs.

### 3.3 Device Detail & Port Faceplate (`/devices/:id`)

**Purpose:** Give a hardware-accurate, capability-driven view of a single device (`DeviceDetail`), centered around an interactive front-panel faceplate that unifies OpenWrt/DSA, RTLPlayground, UniFi, Andaru, pfSense/OPNsense, and SNMP ports.

**Layout:**
1. **Header & Device Switcher:** Breadcrumb (`Devices / sw-core-rack`), inline device dropdown to switch devices without leaving the detail view, driver + role badges, Mgmt IP & Primary MAC in monospace.
2. **Hardware Telemetry Strip:** Model, Firmware (with update badge if `available` differs), Uptime, CPU/Memory/Temperature sensors, and PoE Budget meter (rendered only when `poe_budget != null`).
3. **Interactive Port Faceplate:**
   - Visual switch bezel grouping copper RJ-45 ports separately from SFP/SFP+ optical cages, with a visual **LAG bracket** uniting bonded member ports (`bond0: sfp1 + sfp2`).
   - Each port tile renders: interface name (`lan1`, `sfp1`), negotiated speed (`10G`, `2.5G`, `1G`, `DOWN`), distinct shape/icon indicators (`SFP+`, `⚡ PoE`, `STP BLK`, `DOM`, and `⚠` if the port is involved in a VLAN anomaly).
   - **VLAN Highlight Strip** above the bezel (`Highlight VLAN: None | 10 | 20 | 30 | 40 | 99`): selecting a VLAN overlays crisp `T` (tagged) or `U` (untagged/PVID) badges onto every matching port on the faceplate.
4. **Port Inspector Card (pinned below the faceplate):**
   - Selected port header: interface name, label, oper state, media, speed/duplex, STP state/role, Fiber/SFP Guard badge.
   - **Peer Link:** LLDP/FDB neighbor link (clickable to navigate to the neighbor device).
   - **VLAN Membership:** Explicit `PVID (Untagged)` chip + `Tagged VLANs` chips. If this port is part of a detected leak or dead-end, an inline diagnostic alert explains the issue and links straight to `VLAN Flow`.
   - **Traffic & L1 Settings:** Live RX/TX rates, error/drop counters, EEE, Flow Control (`RX/TX`).
   - **SFP Optical DOM Panel** (when `sfp.present`): Vendor, Part Number, Wavelength, RX/TX dBm with visual threshold range bar (`-20 dBm` warning floor), Temp °C, Voltage V.
   - **PoE Telemetry** (when `poe.supported`): State, Class, live Watts.
5. **Capability-Gated Lower Sections:** Rendered strictly from `device.capabilities`:
   - `cap:segments` / `cap:zones`: L3 Segments & Zones table (addresses, VLAN ID, management marker).
   - `cap:gateway` / `cap:firewall`: Gateways (RTT/loss) + Firewall state & services.
   - `cap:wireless`: Radios (band/channel/width/utilization) & SSID-to-VLAN mapping table.
   - All switches/devices: Compact full **Ports Table** (sortable/scannable complement to the visual faceplate) and **Attached Clients** list.

### 3.4 VLANs & Segments (`/vlans`)

**Purpose:** Give a unified L2 + L3 inventory of every VLAN across the network—bridging firewall `Segments`/`Zones` with switch `PVID`/`TaggedVLANs` and AP `SSIDs`—plus a quick cross-switch audit matrix.

**Layout:**
1. **Sub-view Tabs / Stacked Sections:**
   - **VLAN & Segment Summary Table:** VLAN ID (color-coded chip + numeric ID), Name, Security Zone, Gateway & Subnets (IPv4/IPv6), Devices carrying, Port counts (`Untagged` / `Tagged`), Mapped SSIDs, Active Clients, Health Status (`✓ Clean`, `⊘ Dead-End`, `⚠ Leak`), and a `Trace Flow →` button.
   - **Cross-Device Port Tagging Matrix** (inspired by UniFi VLAN Viewer): Rows = infrastructure ports grouped by device; Columns = VLAN IDs. Each cell shows `U` (untagged/PVID), `T` (tagged), or `·` (none). Cells participating in a PVID mismatch, asymmetric tag, or missing trunk VLAN display a warning/error frame + icon, clickable to open the relevant flow or port. Includes a `Show anomalies only` filter toggle.

### 3.5 VLAN Flow Diagram (`/vlan-flow`) — The Signature View

See **Section 4** below for the complete visual grammar specification.

### 3.6 Clients (`/clients`)

**Purpose:** Fast cross-vendor lookup of wired and wireless endpoints (`Client` model) showing exact switch port or AP/SSID attachment, VLAN, IPv4/IPv6, signal, and reachability warnings (e.g., flagging clients attached to a dead-end VLAN port).

---

## 4. VLAN Flow Diagram — Visual Grammar Specification

Issue `andaru-project/netplane#18` calls for a view that draws the VLAN configuration across devices and reveals its **consequences**: where each VLAN is tagged or untagged port-by-port, which segments and zones it reaches through trunks, LAGs, and wireless uplinks, and where it dead-ends or leaks.

### 4.1 Core Spatial Layout: Left-to-Right Propagation Columns

Unlike the physical Topology map (top-down tree rooted at the gateway), the VLAN Flow diagram uses a **left-to-right layered flow layout** grouped into topological depth columns:

```text
[ Col 0: L3 Zones & Segments ] ──▶ [ Col 1: Core Fabric ] ──▶ [ Col 2: Distribution & Wired APs ] ──▶ [ Col 3: Mesh APs & Access Endpoints ]
```

Why left-to-right works better than a top-down tree here:
- **Horizontal port rails:** Every device card places its **ingress/uplink port badges on its left border** and its **egress/downlink port badges on its right border**. Ribbons connect **port-to-port**, making tagged/untagged transitions at each interface readable left-to-right like a circuit schematic.
- **Clear origin-to-endpoint narrative:** L3 firewall zones & gateway segments anchor the left edge; wireless SSIDs and wired access endpoints terminate the right edge. When a VLAN dead-ends mid-network, the broken link and ghosted unreachable endpoints stand out immediately.

### 4.2 View Modes & Controls

- **Single-VLAN Focus (default when inspecting a VLAN or anomaly):** Isolates one VLAN (`VLAN 10`, `20`, `30`, `40`, or `99`) with full per-port `T`/`U`/`✕` annotations, endpoint lists, and exact hop-by-hop ribbon styling. If that VLAN leaks into another VLAN (e.g., VLAN 10 ↔ VLAN 20), the foreign VLAN's receiving port and zone boundary are pulled into the diagram with a cross-zone hazard highlight so the leak is unmistakable.
- **All VLANs Overview:** Renders parallel color-coded VLAN ribbons across the entire device fabric simultaneously, letting operators see trunk density and spot every flagged link across the network at once. Clicking any ribbon or VLAN pill focuses that VLAN.
- **VLAN Selector Bar:** Each VLAN pill displays its ID, name, and live health indicator (`✓`, `⊘ Dead-end`, `⚠ Leak`).

### 4.3 Visual Grammar Reference

#### Nodes
| Element | Visual Representation | Information Shown |
|---|---|---|
| **L3 Zone & Segment** | Left-anchored card with `4px` left color bar in VLAN hue + shield icon | Zone name (`zone: lan`), segment interface (`bond0.10`), CIDR (`10.10.10.0/24`), gateway device (`fw-core`), management tag if `is_management`. |
| **Infrastructure Device** | Rounded container (`6px` radius), `1px` crisp border, compact header bar | Role icon, device name, driver chip (`andaru`, `openwrt`, `rtlplayground`, `unifi`). Left edge = ingress ports; right edge = egress ports. |
| **Wired Access Group** | Compact terminal pill on right column | Port name(s), attached client hostnames, `U` (untagged) badge. |
| **Wireless SSID Endpoint** | Compact terminal pill with Wi-Fi arc icon | SSID name (`Home-Guest`), bands (`5/6 GHz`), client count, VLAN ID. |
| **Unreachable / Stranded Endpoint** | Dashed border, hatched neutral fill, reduced contrast + `Unreachable` badge | Shows ports or SSIDs *configured* for the VLAN that cannot actually reach the L3 gateway due to an upstream break. |

#### Port Badges (at every port attachment point)
| State | Badge Glyph | Style | Meaning |
|---|---|---|---|
| **Tagged (802.1Q)** | `T` | Filled pill in VLAN hue (high-contrast text) | Port sends/receives frames for this VLAN with an 802.1Q tag. |
| **Untagged / Native (PVID)** | `U` | Outlined/tinted pill with double border + `U` glyph | Port carries this VLAN untagged (`link.pvid == vlan`). |
| **Missing / Pruned** | `✕` | Dashed amber/red pill with `✕` glyph | Expected by downstream or peer port, but absent from this port's PVID and `tagged_vlans`. |
| **Mismatch / Foreign PVID** | `⇄` | Solid red pill with swap glyph | Peer port's PVID maps incoming untagged frames onto a *different* VLAN ID. |

#### Edges (Port-to-Port Ribbons)
| Link / Tag State | Stroke & Pattern | Inline Midpoint Pill |
|---|---|---|
| **Tagged Ethernet Trunk** | Solid smooth cubic Bézier (`3px`) in VLAN hue | `T` (or `T · 2.5G`) |
| **Untagged (PVID) Link** | Parallel hollow-rail / double-line stroke in VLAN hue | `U` (explicitly distinguishes untagged wire traffic from tagged!) |
| **LAG / LACP Bonded Trunk** | Thick twin-rail stroke (`5px` outer + center channel) | `LAG · bond0 · 20G` |
| **Wireless Mesh Uplink** | Fine dashed curve (`5 4` dash) | `Wi-Fi · -63 dBm` |
| **STP Blocked Redundant Link** | Muted gray short-dash (`3 3`) with center stop bar `┃` | `STP BLK` (informational, not an anomaly) |

#### Problem Flags (Consequences)
Problems are derived deterministically from `TopologyGraph` + `Link.{pvid, tagged_vlans}` + `Segment` + `Zone` + `WirelessRadio.ssids`:

1. **Cross-VLAN Leak — PVID Mismatch (`severity: crit`, `kind: leak`)**
   - **Condition:** Connected trunk/link ports `A` and `B` have `A.pvid != B.pvid` (where both are active non-isolated switch/router links and at least one carries an active VLAN).
   - **Visual:** The ribbon **transitions hue mid-span** using an SVG linear gradient (`VLAN A hue → red hazard band → VLAN B hue`) overlaid with diagonal hazard ticks and a high-contrast red callout pill (`⚠ LEAK · PVID 10 → 20`). Downstream bridged ports show a subtle red cross-zone tag (`lan ⇄ iot`).
   - **Inspector output:** Side-by-side port config comparison, explicit security consequence (*"Untagged frames from VLAN 10 (`zone: lan`) cross `sw-core:lan2` untagged and enter `sw-media:port1` as VLAN 20 (`zone: iot`), bypassing `fw-core` firewall policy"*), affected devices/clients, and remediation guidance.

2. **Dead-End — Missing on Trunk (`severity: warn`, `kind: dead_end`)**
   - **Condition:** Downstream device `B` configures VLAN `V` (tagged on its uplink, on access ports, or on an SSID), OR upstream `A` sends `V` into `B` which drops it—so `V` is present on one side of a link (`A` or `B`) but missing on the other.
   - **Visual:** Ribbon approaches the break and hits a crisp amber/red **vertical stop-bar (`┫`)** with a callout badge (`⊘ DEAD-END · Missing on sw-core:lan1`). The disconnected segment beyond the stop-bar renders as a faded dashed line into hatched `Unreachable` endpoint cards so the user immediately sees *what is stranded* (`bench-dut-01`, `bench-dut-02`).

3. **Dead-End — Missing on Wireless Uplink (`severity: warn`, `kind: dead_end`)**
   - **Condition:** A mesh AP advertises an SSID mapped to VLAN `V` (or bridges a wired port on `V`), while its `WirelessUplink` backhaul does not carry `V`.
   - **Visual:** Dashed wireless arc terminates at a stop-bar (`⊘ DEAD-END · Not on mesh uplink`), and the affected SSID pill (`Home-Guest`) displays `0 reachable · No gateway path`.

4. **Asymmetric Tagging / Untagged in Two Places (`severity: warn` or `crit`, `kind: untagged_conflict`)**
   - **Condition:** VLAN `V` is transmitted **untagged** (`pvid = V`) on one end of an inter-device link while the peer expects `V` **tagged** (`V ∈ tagged_vlans`), or two distinct VLANs share untagged ingress/egress across a link (which often co-occurs with a PVID mismatch).
   - **Visual:** Port badges on either end of the link highlight the asymmetry (`U 10` vs `T 10`), and the inspector explicitly calls out both the tagging mismatch and any resulting silent drop or cross-VLAN bleed.

---

## 5. Research & Inspiration

We studied **UniFi Network**, **Cisco Meraki Dashboard**, **TP-Link Omada SDN Controller**, **NetBox / NetBox Visual Explorer**, **Forward Networks**, **Batfish**, and flow-visualization tools (**Netdata**).

### 5.1 Network Managers Studied

| Tool | Sources | What Works Well & What We Borrowed | What We Avoided / Improved |
|---|---|---|---|
| **Ubiquiti UniFi Network** | [VLAN Troubleshooting & VLAN Viewer](https://help.ui.com/hc/en-us/articles/9592924981911-Virtual-Network-VLAN-Troubleshooting)<br>[Switch Port VLAN Assignment](https://help.ui.com/hc/en-us/articles/26136855808919-Switch-Port-VLAN-Assignment-Trunk-Access-Ports)<br>[Introduction to UniFi](https://help.ui.com/hc/en-us/articles/360012192813-Introduction-to-UniFi) | **Port Manager + Faceplate** linking physical port layout to native/tagged VLANs and PoE; **VLAN Viewer** giving a compact cross-port tag matrix for quick auditing; clean device/client hierarchy. | UniFi's Topology stays physical-only, and VLAN Viewer is a flat per-switch grid that does not trace multi-hop paths or cross-vendor leaks. Netplane keeps the cross-port matrix inside `VLANs & Segments` and adds the full multi-hop `VLAN Flow` view. |
| **Cisco Meraki Dashboard** | [Switch Port View (Faceplate)](https://documentation.meraki.com/Switching/MS_-_Switches/Operate_and_Maintain/Monitoring_and_Reporting/Switch_Port_View)<br>[Network Topology](https://documentation.meraki.com/Switching/MS_-_Switches/Operate_and_Maintain/Monitoring_and_Reporting/Network_Topology) | **Distinct geometric shapes per device role** in Topology (firewall vs. switch vs. AP vs. third-party); **Color Assist Mode** (never relying on green/red color alone on switch faceplates); clear Access vs. Trunk port inspection with speed/STP/PoE indicators. | Avoided Meraki's heavy chrome and cloud-only assumptions; adapted Color Assist principles natively into default iconography and text labels (`10G`, `STP BLK`, `DOM`). |
| **TP-Link Omada SDN (v6.2+)** | [Omada VLAN Topology Preview](https://support.omadanetworks.com/en/document/110874/) | **VLAN Topology Overlay** directly on the topology map: selecting a VLAN colors links green (passes both ends), red (blocked/mismatched), or gray (unused), and uses solid lines for wired vs. dashed lines for wireless SSIDs. | Omada's overlay only shows binary pass/block on a single-vendor tree and doesn't display tagged vs. untagged state per port or PVID cross-VLAN leaks. We adopt the VLAN overlay toggle inside `Topology` *and* go much deeper in `VLAN Flow`. |
| **NetBox & NetBox Visual Explorer** | [NetBox Visual Explorer](https://netboxlabs.com/blog/netbox-visual-explorer-network-visualization/) | Strict separation between **physical cable path** and **L2/L3 dependency view** (VLANs, interfaces, IP prefixes); tabular step-by-step hop clarity alongside visual graphs. | NetBox models documented intent (source of truth), not live observed FDB/LLDP/PVID mismatches. Netplane pairs live observed state with explicit consequence diagnostics. |

### 5.2 Prior Art Search for the VLAN Flow Diagram

We searched for existing tools that visualize VLAN configuration propagation, Sankey/flow-style VLAN diagrams, and L2 leak/dead-end tracing. **Result: no consumer, homelab, or open-source network manager implements a dedicated multi-vendor VLAN configuration flow diagram showing per-port tagged/untagged propagation alongside dead-end and cross-VLAN leak consequences.**

The closest partial equivalents found live in three separate categories:
1. **TP-Link Omada v6.2 VLAN Topology Preview** ([omadanetworks.com/en/document/110874](https://support.omadanetworks.com/en/document/110874/)): Closest in SMB/homelab UX—overlays per-VLAN pass/fail coloring onto the topology graph—but lacks per-port `T`/`U` tagging visibility, L3 zone endpoints, and PVID mismatch leak modeling.
2. **Forward Networks Path Search** ([Path Search Quick Reference](https://community.forwardnetworks.com/path-search-85/unlocking-the-power-of-network-path-search-a-quick-reference-guide-500), [Mathematical Model White Paper](https://www.forwardnetworks.com/blog/lf/mathematical-model-white-paper/)): Closest conceptual grammar—renders left-to-right hop-by-hop flows showing exact L2 header transformations (VLAN tag push/pop and PVID rewrites across switches). However, it is an enterprise digital-twin tool driven by point-to-point packet queries rather than a whole-VLAN propagation view. **Borrowed:** Left-to-right hop progression and explicit mid-wire header/VLAN transformation callouts for PVID leaks.
3. **Netdata / SolarWinds Sankey Flow Analyzers** ([Netdata NetFlow Analyzer](https://www.netdata.cloud/features/network/netflow-traffic-analyzer/)): Use Sankey ribbons to visualize *traffic volume* across IPs/ports/VLANs, not *L2 configuration reachability or misconfigurations*. **Borrowed:** Visual rhythm of clean, parallel multi-lane Bézier ribbons for the `All VLANs` overview mode (using uniform semantic stroke widths rather than volume-scaled widths, since configuration reachability matters equally on idle and busy links).

---

## 6. Open Questions

1. **Backend vs. Frontend VLAN Analysis Engine:** Should the VLAN propagation graph and anomaly detector (`dead_end`, `pvid_mismatch` / `leak`, `untagged_conflict`) live as a deterministic pure-Go solver alongside `internal/topology/solver.go` (exposed via e.g. `GET /api/v1/vlans/flow`), so both the Web UI and Home Assistant integration / CLI can consume findings? *(Recommendation: Yes—a Go `internal/vlanflow` solver keeps the UI thin, testable against golden multi-vendor fixtures, and allows SSE events like `vlan.anomaly_detected`.)*
2. **Wireless Uplink VLAN Membership Gaps across Drivers:** Not every driver reports the exact allowed VLAN list on a wireless mesh uplink (UniFi exposes AP/WLAN VLANs and uplink peers, while SNMP/OpenWrt mesh drivers may only expose SSIDs and bridge VLANs). How should `VLAN Flow` represent *unknown/unreported* VLAN transport on an edge vs. *confirmed missing*? *(Recommendation: Add a fourth edge state `? Unverified` with a gray dotted pill so Netplane never cries wolf on partial driver capabilities.)*
3. **Untagged VLAN 1 Default Ambiguity:** Many unmanaged or minimally configured ports report `PVID 1` by default even when no L3 segment exists for VLAN 1. Should VLAN 1 be hidden by default unless it carries an L3 segment, active clients, or participates in a mismatch?
4. **Native Andaru Hosting & Navigation Embedding (`netplane#17`):** When Netplane runs embedded inside Andaru's own web UI (versus standalone or Home Assistant Ingress), should Netplane's sidebar collapse into a `Netplane` sub-section inside Andaru's left nav, or mount as an app switcher next to the Andaru header mark?
5. **Scale Beyond 10–15 Devices:** The left-to-right column layout works cleanly for typical home/small-office networks (1 firewall, 2–5 switches, 2–6 APs). For larger networks (15+ switches), should `All VLANs` collapse leaf access ports into summary count chips by default and add pan/zoom controls?
6. **Phase 2 Direct Remediation:** Once `ConfigDriver` lands in Phase 2, should clicking a leak or dead-end in `VLAN Flow` offer a `"Draft fix (Save → Preview)"` action that stages the exact port PVID/tagged edits across the affected switches into Andaru's/Netplane's safe apply review queue?
