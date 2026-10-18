import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import type { VPPGraph } from "../types";

const FILL_COLORS = [
  "#ffffcc",
  "#ffeda0",
  "#fed976",
  "#feb24c",
  "#fd8d3c",
  "#fc4e2a",
  "#e31a1c",
  "#b10026",
];

const NODE_SPACING = 92;
const LAYER_GAP = 104;
const PADDING = 60;
const MIN_WIDTH = 900;
const MIN_VIEW_WIDTH = 220;

const INTERFACE_NODE =
  /^(?:vhost-user-input|tap-input|af-packet-input|interface-output|interface-\d+-(?:output|tx)(?:-deleted)?|(?:bvi|host|vm|vnf)[^ ]*-(?:output|tx)(?:-deleted)?)$/;

type LayoutNode = {
  id: string;
  label: string;
  x: number;
  y: number;
  radius: number;
  color: string;
  rank: number;
  isInterface: boolean;
};

type LayoutEdge = {
  source: string;
  target: string;
  backward: boolean;
};

type Layout = {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  width: number;
  height: number;
  maxRank: number;
  maxLayerCount: number;
  backwardCount: number;
  interfaceCount: number;
};

type ViewBox = {
  x: number;
  y: number;
  w: number;
  h: number;
};

type PanState = {
  pointerId: number;
  startX: number;
  startY: number;
  startView: ViewBox;
  scaleX: number;
  scaleY: number;
};

type IndexedEdge = {
  sourceIndex: number;
  targetIndex: number;
  source: string;
  target: string;
};

type FocusGroup = {
  id: string;
  label: string;
  nodeIDs: string[];
};

function interfaceGroupID(nodeID: string) {
  const match = /^(.*?)-(?:output|tx)(?:-deleted)?$/.exec(nodeID);
  return match ? match[1] : nodeID;
}

function interfaceGroupLabel(groupID: string) {
  if (groupID === "vhost-user-input") {
    return "vhost-user input";
  }
  if (groupID === "tap-input") {
    return "tap input";
  }
  if (groupID === "af-packet-input") {
    return "af-packet input";
  }
  if (groupID === "interface") {
    return "interface (generic)";
  }
  if (/^interface-\d+$/.test(groupID)) {
    return `${groupID} (deleted)`;
  }
  if (groupID.startsWith("vm-")) {
    return `${groupID} (VM interface)`;
  }
  if (/^vnf/.test(groupID)) {
    return `${groupID} (VNF tap)`;
  }
  if (/^bvi/.test(groupID)) {
    return `${groupID} (BVI)`;
  }
  if (groupID.startsWith("host-")) {
    return `${groupID} (host NIC)`;
  }
  return groupID;
}

function buildInterfaceGroups(layout: Layout): FocusGroup[] {
  const groups = new Map<string, string[]>();
  for (const node of layout.nodes) {
    if (!node.isInterface) {
      continue;
    }
    const id = interfaceGroupID(node.id);
    const existing = groups.get(id);
    if (existing) {
      existing.push(node.id);
    } else {
      groups.set(id, [node.id]);
    }
  }
  return Array.from(groups.entries())
    .map(([id, nodeIDs]) => ({ id, label: interfaceGroupLabel(id), nodeIDs }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

function buildFeatureGroups(layout: Layout): FocusGroup[] {
  const groups: FocusGroup[] = [];
  const add = (id: string, label: string, matches: (nodeID: string) => boolean) => {
    const nodeIDs = layout.nodes.filter((node) => matches(node.id)).map((node) => node.id);
    if (nodeIDs.length > 0) {
      groups.push({ id, label, nodeIDs });
    }
  };
  add("feature:acl", "ACL plugin", (id) => id.startsWith("acl-plugin"));
  add("feature:nat", "NAT44", (id) => id.startsWith("nat"));
  add("feature:dhcp", "DHCP", (id) => id.includes("dhcp"));
  return groups;
}

function buildAdjacency(layout: Layout) {
  const neighbors = new Map<string, Set<string>>();
  for (const node of layout.nodes) {
    neighbors.set(node.id, new Set([node.id]));
  }
  for (const edge of layout.edges) {
    neighbors.get(edge.source)?.add(edge.target);
    neighbors.get(edge.target)?.add(edge.source);
  }
  return neighbors;
}

function layoutGraph(graph: VPPGraph): Layout {
  const rawNodes = Array.isArray(graph.nodes) ? graph.nodes : [];
  const rawEdges = Array.isArray(graph.edges) ? graph.edges : [];
  const empty: Layout = {
    nodes: [],
    edges: [],
    width: MIN_WIDTH,
    height: 420,
    maxRank: 0,
    maxLayerCount: 0,
    backwardCount: 0,
    interfaceCount: 0,
  };
  if (rawNodes.length === 0) {
    return empty;
  }

  const nodeIndex = new Map<string, number>();
  rawNodes.forEach((node, index) => nodeIndex.set(node.id, index));
  const nodeCount = rawNodes.length;
  const outgoing: number[][] = Array.from({ length: nodeCount }, () => []);
  const incoming: number[][] = Array.from({ length: nodeCount }, () => []);
  const validEdges: IndexedEdge[] = [];
  const seenEdges = new Set<string>();

  for (const edge of rawEdges) {
    const sourceIndex = nodeIndex.get(edge.source);
    const targetIndex = nodeIndex.get(edge.target);
    if (sourceIndex === undefined || targetIndex === undefined) {
      continue;
    }
    const key = `${sourceIndex}\u0000${targetIndex}`;
    if (seenEdges.has(key)) {
      continue;
    }
    seenEdges.add(key);
    validEdges.push({
      sourceIndex,
      targetIndex,
      source: edge.source,
      target: edge.target,
    });
    // Self-loops do not affect layering but are real VPP next-nodes and are
    // rendered as small feedback loops above the node.
    if (sourceIndex === targetIndex) {
      continue;
    }
    outgoing[sourceIndex].push(targetIndex);
    incoming[targetIndex].push(sourceIndex);
  }

  // VPP has a few terminal nodes (`error-drop`, then `drop`) that are
  // reachable from almost everywhere. Remove the sink and its immediate
  // predecessors from the forward BFS so they can be placed at the bottom
  // instead of appearing beside the input nodes.
  const sinks = new Set<number>();
  for (let index = 0; index < nodeCount; index += 1) {
    if (outgoing[index].length === 0) {
      sinks.add(index);
    }
  }
  const preSinks = new Set<number>();
  for (let index = 0; index < nodeCount; index += 1) {
    if (outgoing[index].length > 0 && outgoing[index].every((target) => sinks.has(target))) {
      preSinks.add(index);
    }
  }

  const core: number[] = [];
  for (let index = 0; index < nodeCount; index += 1) {
    if (!sinks.has(index) && !preSinks.has(index)) {
      core.push(index);
    }
  }
  const coreSet = new Set(core);
  const coreOutgoing = new Map<number, number[]>();
  const coreIncoming = new Map<number, number[]>();
  for (const index of core) {
    coreOutgoing.set(
      index,
      outgoing[index].filter((target) => coreSet.has(target))
    );
    coreIncoming.set(
      index,
      incoming[index].filter((source) => coreSet.has(source))
    );
  }

  // Breadth-first layers from the input side. Cycles are harmless here:
  // each node is visited at its shortest distance from an input.
  const coreRank = new Map<number, number>();
  const visited = new Set<number>();
  const walk = (start: number, startRank: number) => {
    const queue = [start];
    visited.add(start);
    coreRank.set(start, startRank);
    for (let head = 0; head < queue.length; head += 1) {
      const node = queue[head];
      const rank = coreRank.get(node) ?? 0;
      for (const target of coreOutgoing.get(node) ?? []) {
        if (!visited.has(target)) {
          visited.add(target);
          coreRank.set(target, rank + 1);
          queue.push(target);
        }
      }
    }
  };

  const sources = core.filter((index) => (coreIncoming.get(index)?.length ?? 0) === 0);
  if (sources.length === 0 && core.length > 0) {
    sources.push(core[0]);
  }
  for (const source of sources) {
    if (!visited.has(source)) {
      walk(source, 0);
    }
  }
  for (const index of core) {
    if (!visited.has(index)) {
      walk(index, 0);
    }
  }

  let coreMax = 0;
  for (const value of coreRank.values()) {
    coreMax = Math.max(coreMax, value);
  }
  const preSinkRank = coreMax + 1;
  const sinkRank = coreMax + (preSinks.size > 0 ? 2 : 1);

  const rank: number[] = new Array(nodeCount).fill(0);
  for (const index of core) {
    rank[index] = coreRank.get(index) ?? 0;
  }
  for (const index of preSinks) {
    rank[index] = preSinkRank;
  }
  for (const index of sinks) {
    rank[index] = sinkRank;
  }

  let maxRank = 0;
  for (const value of rank) {
    maxRank = Math.max(maxRank, value);
  }
  const layers: number[][] = Array.from({ length: maxRank + 1 }, () => []);
  for (let index = 0; index < nodeCount; index += 1) {
    layers[rank[index]].push(index);
  }

  const labelOf = (index: number) => rawNodes[index].label || rawNodes[index].id;
  layers.forEach((layer) => {
    layer.sort((a, b) => labelOf(a).localeCompare(labelOf(b)));
  });

  const positionInLayer = new Array<number>(nodeCount).fill(0);
  const updatePositions = () => {
    layers.forEach((layer) => {
      layer.forEach((node, index) => {
        positionInLayer[node] = index;
      });
    });
  };
  updatePositions();

  const normalizedPosition = (index: number) => {
    const layer = layers[rank[index]];
    return layer.length <= 1 ? 0.5 : positionInLayer[index] / (layer.length - 1);
  };

  // Barycenter sweeps reduce edge crossings without an external layout
  // library. Five passes is enough for graphs this size.
  for (let iteration = 0; iteration < 5; iteration += 1) {
    for (let row = 1; row <= maxRank; row += 1) {
      const layer = layers[row];
      const barycenter = new Map<number, number>();
      for (const node of layer) {
        const neighbors = incoming[node].filter((source) => rank[source] < row);
        barycenter.set(
          node,
          neighbors.length > 0
            ? neighbors.reduce((sum, source) => sum + normalizedPosition(source), 0) /
                neighbors.length
            : normalizedPosition(node)
        );
      }
      layer.sort(
        (a, b) =>
          (barycenter.get(a) ?? 0) - (barycenter.get(b) ?? 0) ||
          labelOf(a).localeCompare(labelOf(b))
      );
      updatePositions();
    }

    for (let row = maxRank - 1; row >= 0; row -= 1) {
      const layer = layers[row];
      const barycenter = new Map<number, number>();
      for (const node of layer) {
        const neighbors = outgoing[node].filter((target) => rank[target] > row);
        barycenter.set(
          node,
          neighbors.length > 0
            ? neighbors.reduce((sum, target) => sum + normalizedPosition(target), 0) /
                neighbors.length
            : normalizedPosition(node)
        );
      }
      layer.sort(
        (a, b) =>
          (barycenter.get(a) ?? 0) - (barycenter.get(b) ?? 0) ||
          labelOf(a).localeCompare(labelOf(b))
      );
      updatePositions();
    }
  }

  const maxLayerCount = Math.max(...layers.map((layer) => layer.length), 1);
  const width = Math.max(MIN_WIDTH, (maxLayerCount - 1) * NODE_SPACING + PADDING * 2);
  const height = Math.max(420, maxRank * LAYER_GAP + PADDING * 2);
  const x = new Array<number>(nodeCount).fill(width / 2);
  const y = new Array<number>(nodeCount).fill(PADDING);

  layers.forEach((layer, row) => {
    const span = Math.min(width - PADDING * 2, Math.max(0, (layer.length - 1) * NODE_SPACING));
    const start = width / 2 - span / 2;
    layer.forEach((node, index) => {
      x[node] = layer.length === 1 ? width / 2 : start + (span * index) / (layer.length - 1);
      y[node] = PADDING + row * LAYER_GAP;
    });
  });

  const widths = rawNodes.map((node) => Number(node.width) || 0);
  const minWidth = Math.min(...widths);
  const maxWidth = Math.max(...widths);
  const nodes: LayoutNode[] = rawNodes.map((node, index) => {
    const fill = Number(node.fill) || 0;
    const scale = maxWidth > minWidth ? (widths[index] - minWidth) / (maxWidth - minWidth) : 0.5;
    return {
      id: node.id,
      label: node.label || node.id,
      x: x[index],
      y: y[index],
      radius: 7 + scale * 11,
      color: fill >= 1 && fill <= FILL_COLORS.length ? FILL_COLORS[fill - 1] : "#64748b",
      rank: rank[index],
      isInterface: INTERFACE_NODE.test(node.id),
    };
  });

  const edges: LayoutEdge[] = validEdges.map((edge) => ({
    source: edge.source,
    target: edge.target,
    backward: rank[edge.sourceIndex] >= rank[edge.targetIndex],
  }));

  return {
    nodes,
    edges,
    width,
    height,
    maxRank,
    maxLayerCount,
    backwardCount: edges.filter((edge) => edge.backward).length,
    interfaceCount: nodes.filter((node) => node.isInterface).length,
  };
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

export function VPPGraphView({ graph }: { graph: VPPGraph }) {
  const allLayout = useMemo(() => layoutGraph(graph), [graph]);
  const [selectedInterface, setSelectedInterface] = useState("");
  const interfaceGroups = useMemo(() => buildInterfaceGroups(allLayout), [allLayout]);
  const featureGroups = useMemo(() => buildFeatureGroups(allLayout), [allLayout]);
  const focusGroups = useMemo(
    () => [...interfaceGroups, ...featureGroups],
    [interfaceGroups, featureGroups]
  );
  const allAdjacency = useMemo(() => buildAdjacency(allLayout), [allLayout]);
  const focusedIDs = useMemo(() => {
    if (!selectedInterface) {
      return null;
    }
    const group = focusGroups.find((item) => item.id === selectedInterface);
    if (!group) {
      return null;
    }
    const visible = new Set(group.nodeIDs);
    for (const nodeID of group.nodeIDs) {
      for (const neighbor of allAdjacency.get(nodeID) ?? []) {
        visible.add(neighbor);
      }
    }
    return visible;
  }, [selectedInterface, focusGroups, allAdjacency]);
  const displayGraph = useMemo(() => {
    if (!focusedIDs) {
      return graph;
    }
    return {
      ...graph,
      nodes: graph.nodes.filter((node) => focusedIDs.has(node.id)),
      edges: graph.edges.filter(
        (edge) => focusedIDs.has(edge.source) && focusedIDs.has(edge.target)
      ),
    };
  }, [graph, focusedIDs]);
  const layout = useMemo(() => layoutGraph(displayGraph), [displayGraph]);
  const [view, setView] = useState<ViewBox>(() => ({
    x: 0,
    y: 0,
    w: layout.width,
    h: layout.height,
  }));
  const [panning, setPanning] = useState(false);
  const [hovered, setHovered] = useState<string | null>(null);
  const [showAllLabels, setShowAllLabels] = useState(false);
  const svgRef = useRef<SVGSVGElement | null>(null);
  const panRef = useRef<PanState | null>(null);

  useEffect(() => {
    if (selectedInterface && !focusGroups.some((group) => group.id === selectedInterface)) {
      setSelectedInterface("");
    }
  }, [focusGroups, selectedInterface]);

  useEffect(() => {
    panRef.current = null;
    setPanning(false);
    setHovered(null);
    setView({ x: 0, y: 0, w: layout.width, h: layout.height });
  }, [layout]);

  const clampView = (candidate: ViewBox): ViewBox => {
    const margin = 40;
    let { x, y, w, h } = candidate;
    if (w >= layout.width) {
      x = (layout.width - w) / 2;
    } else {
      x = clamp(x, -margin, layout.width - w + margin);
    }
    if (h >= layout.height) {
      y = (layout.height - h) / 2;
    } else {
      y = clamp(y, -margin, layout.height - h + margin);
    }
    return { x, y, w, h };
  };

  const zoomView = (factor: number, centerX: number, centerY: number) => {
    setView((current) => {
      const minWidth = Math.max(MIN_VIEW_WIDTH, layout.width * 0.06);
      const maxWidth = layout.width * 1.08;
      const targetWidth = clamp(current.w / factor, minWidth, maxWidth);
      const ratio = targetWidth / current.w;
      const w = current.w * ratio;
      const h = current.h * ratio;
      return clampView({
        x: centerX - (centerX - current.x) * ratio,
        y: centerY - (centerY - current.y) * ratio,
        w,
        h,
      });
    });
  };

  const clientToSVG = (clientX: number, clientY: number) => {
    const svg = svgRef.current;
    if (!svg) {
      return { x: 0, y: 0 };
    }
    const matrix = svg.getScreenCTM();
    if (!matrix) {
      return { x: 0, y: 0 };
    }
    const point = svg.createSVGPoint();
    point.x = clientX;
    point.y = clientY;
    const transformed = point.matrixTransform(matrix.inverse());
    return { x: transformed.x, y: transformed.y };
  };

  const resetView = () => {
    setView({ x: 0, y: 0, w: layout.width, h: layout.height });
  };

  const handleWheel = (event: WheelEvent) => {
    event.preventDefault();
    const point = clientToSVG(event.clientX, event.clientY);
    zoomView(Math.exp(-event.deltaY * 0.0015), point.x, point.y);
  };

  const handlePointerDown = (event: PointerEvent) => {
    if (event.pointerType === "mouse" && event.button !== 0) {
      return;
    }
    const svg = svgRef.current;
    if (!svg) {
      return;
    }
    const matrix = svg.getScreenCTM();
    if (!matrix) {
      return;
    }
    event.preventDefault();
    svg.setPointerCapture(event.pointerId);
    panRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      startView: view,
      scaleX: matrix.a || 1,
      scaleY: matrix.d || 1,
    };
    setPanning(true);
  };

  const handlePointerMove = (event: PointerEvent) => {
    const pan = panRef.current;
    if (!pan || pan.pointerId !== event.pointerId) {
      return;
    }
    const dx = (pan.startX - event.clientX) / pan.scaleX;
    const dy = (pan.startY - event.clientY) / pan.scaleY;
    setView(
      clampView({
        ...pan.startView,
        x: pan.startView.x + dx,
        y: pan.startView.y + dy,
      })
    );
  };

  const endPan = (event: PointerEvent) => {
    const pan = panRef.current;
    if (!pan || pan.pointerId !== event.pointerId) {
      return;
    }
    panRef.current = null;
    setPanning(false);
    const svg = svgRef.current;
    if (svg?.hasPointerCapture(event.pointerId)) {
      svg.releasePointerCapture(event.pointerId);
    }
  };

  const nodeByID = useMemo(
    () => new Map(layout.nodes.map((node) => [node.id, node])),
    [layout]
  );

  const adjacency = useMemo(() => buildAdjacency(layout), [layout]);

  if (layout.nodes.length === 0) {
    return (
      <div class="empty vpp-graph-empty">
        <div class="empty-mark" aria-hidden="true">
          ◌
        </div>
        <h3>No active VPP nodes</h3>
        <p>Generate traffic or refresh after VPP has processed packets since the last stats clear.</p>
      </div>
    );
  }

  const showLabels = layout.nodes.length <= 40 || showAllLabels;
  const showNodeLabels = showLabels || hovered !== null;
  const visibleIDs = hovered ? adjacency.get(hovered) ?? new Set([hovered]) : null;
  const neighborCount = hovered && visibleIDs ? Math.max(0, visibleIDs.size - 1) : 0;
  const labelSize = Math.max(9, Math.min(26, view.w / 70));
  const zoomPercent = Math.max(1, Math.round((layout.width / view.w) * 100));
  const activeNode = hovered ? nodeByID.get(hovered) : null;

  return (
    <div class="vpp-graph-wrap">
      <div class="vpp-graph-meta">
        <span>
          {layout.nodes.length} active node{layout.nodes.length === 1 ? "" : "s"} · {layout.edges.length}{" "}
          edge{layout.edges.length === 1 ? "" : "s"}
          {layout.backwardCount > 0 ? ` · ${layout.backwardCount} feedback` : ""}
          {layout.interfaceCount > 0
            ? ` · ${layout.interfaceCount} interface${layout.interfaceCount === 1 ? "" : "s"}`
            : ""}
        </span>
        <span class="vpp-graph-legend">
          <i class="vpp-legend-node"></i>node = cycles/packet
          <i class="vpp-legend-packet"></i>color = packets/call
          <i class="vpp-legend-interface"></i>ring = interface
          {activeNode
            ? ` · ${activeNode.label}${activeNode.isInterface ? " (interface)" : ""} · ${neighborCount} connected`
            : " · hover to isolate connections"}
        </span>
      </div>

      <div class="vpp-graph-toolbar">
        <span class="vpp-graph-hint">
          Inputs at top · outputs at bottom · drag to pan · scroll to zoom
          {layout.backwardCount > 0 ? " · dashed edge = feedback/self-loop" : ""}
        </span>
        <div class="vpp-graph-controls">
          {focusGroups.length > 0 ? (
            <label class="vpp-interface-picker">
              <span>Focus</span>
              <select
                value={selectedInterface}
                title="Show only nodes related to this interface or packet feature"
                onChange={(event) => setSelectedInterface(event.currentTarget.value)}
              >
                <option value="">All nodes</option>
                {interfaceGroups.length > 0 ? (
                  <optgroup label="Interfaces">
                    {interfaceGroups.map((group) => (
                      <option key={group.id} value={group.id}>
                        {group.label}
                      </option>
                    ))}
                  </optgroup>
                ) : null}
                {featureGroups.length > 0 ? (
                  <optgroup label="Packet features">
                    {featureGroups.map((group) => (
                      <option key={group.id} value={group.id}>
                        {group.label}
                      </option>
                    ))}
                  </optgroup>
                ) : null}
              </select>
            </label>
          ) : null}
          <div class="vpp-graph-zoom">
            <button
              type="button"
              aria-label="Zoom out"
              title="Zoom out"
              onClick={() => zoomView(1 / 1.35, view.x + view.w / 2, view.y + view.h / 2)}
            >
              −
            </button>
            <span>{zoomPercent}%</span>
            <button
              type="button"
              aria-label="Zoom in"
              title="Zoom in"
              onClick={() => zoomView(1.35, view.x + view.w / 2, view.y + view.h / 2)}
            >
              +
            </button>
            <button type="button" onClick={resetView}>
              Fit
            </button>
            {layout.nodes.length > 40 ? (
              <button
                type="button"
                aria-pressed={showAllLabels}
                class={showAllLabels ? "is-active" : ""}
                onClick={() => setShowAllLabels((value) => !value)}
              >
                Labels
              </button>
            ) : null}
          </div>
        </div>
      </div>

      <svg
        ref={svgRef}
        class={`vpp-graph${panning ? " is-panning" : ""}`}
        viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`}
        role="img"
        aria-label="Active VPP packet processing graph, inputs at top and outputs at bottom"
        onWheel={handleWheel}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={endPan}
        onPointerCancel={endPan}
        onLostPointerCapture={() => {
          panRef.current = null;
          setPanning(false);
        }}
      >
        <defs>
          <marker
            id="vpp-graph-arrow"
            viewBox="0 0 10 10"
            refX="9"
            refY="5"
            markerWidth="6"
            markerHeight="6"
            orient="auto-start-reverse"
          >
            <path d="M 0 0 L 10 5 L 0 10 z" fill="#64748b" />
          </marker>
        </defs>
        <g class="vpp-graph-edges">
          {layout.edges.map((edge, index) => {
            const source = nodeByID.get(edge.source);
            const target = nodeByID.get(edge.target);
            if (!source || !target) {
              return null;
            }
            const active = hovered !== null && (edge.source === hovered || edge.target === hovered);
            const hidden = hovered !== null && !active;
            if (edge.source === edge.target) {
              const loop = source.radius + 6;
              return (
                <path
                  key={`${edge.source}->${edge.target}-${index}`}
                  d={`M ${source.x - source.radius} ${source.y} C ${source.x - loop * 2.2} ${
                    source.y - loop * 2.4
                  }, ${source.x + loop * 2.2} ${source.y - loop * 2.4}, ${
                    source.x + source.radius
                  } ${source.y}`}
                  class={`vpp-edge vpp-edge-back${active ? " vpp-edge-active" : ""}${
                    hidden ? " vpp-edge-hidden" : ""
                  }`}
                  markerEnd="url(#vpp-graph-arrow)"
                  vectorEffect="non-scaling-stroke"
                />
              );
            }
            const dx = target.x - source.x;
            const dy = target.y - source.y;
            const distance = Math.max(1, Math.sqrt(dx * dx + dy * dy));
            const sourceX = source.x + (dx / distance) * (source.radius + 2);
            const sourceY = source.y + (dy / distance) * (source.radius + 2);
            const targetX = target.x - (dx / distance) * (target.radius + 5);
            const targetY = target.y - (dy / distance) * (target.radius + 5);
            const direction = targetY >= sourceY ? 1 : -1;
            const curve = Math.max(24, Math.min(80, Math.abs(targetY - sourceY) * 0.45));
            return (
              <path
                key={`${edge.source}->${edge.target}-${index}`}
                d={`M ${sourceX} ${sourceY} C ${sourceX} ${sourceY + direction * curve}, ${targetX} ${
                  targetY - direction * curve
                }, ${targetX} ${targetY}`}
                class={`vpp-edge${edge.backward ? " vpp-edge-back" : ""}${
                  active ? " vpp-edge-active" : ""
                }${hidden ? " vpp-edge-hidden" : ""}`}
                markerEnd="url(#vpp-graph-arrow)"
                vectorEffect="non-scaling-stroke"
              />
            );
          })}
        </g>
        <g class="vpp-graph-nodes">
          {layout.nodes.map((node) => {
            const labelAnchor =
              node.x < 160 ? "start" : node.x > layout.width - 160 ? "end" : "middle";
            const labelX =
              labelAnchor === "start"
                ? node.x - node.radius
                : labelAnchor === "end"
                  ? node.x + node.radius
                  : node.x;
            const visible = visibleIDs === null || visibleIDs.has(node.id);
            return (
              <g
                key={node.id}
                class={`${showNodeLabels ? "vpp-node" : "vpp-node vpp-node-hover-label"}${
                  visible ? "" : " vpp-node-hidden"
                }`}
                onMouseEnter={() => setHovered(node.id)}
                onMouseLeave={() => setHovered(null)}
              >
                {node.isInterface ? (
                  <circle
                    class="vpp-interface-ring"
                    cx={node.x}
                    cy={node.y}
                    r={node.radius + 4}
                    vectorEffect="non-scaling-stroke"
                  />
                ) : null}
                <circle
                  cx={node.x}
                  cy={node.y}
                  r={node.radius}
                  fill={node.color}
                  vectorEffect="non-scaling-stroke"
                >
                  <title>
                    {node.label}
                    {node.isInterface ? " (interface)" : ""}
                  </title>
                </circle>
                <text
                  x={labelX}
                  y={node.y + node.radius + labelSize * 1.25}
                  textAnchor={labelAnchor}
                  style={{ fontSize: `${labelSize}px` }}
                >
                  {node.label}
                </text>
              </g>
            );
          })}
        </g>
      </svg>

      <div class="vpp-graph-palette" aria-hidden="true">
        <span>packets/call</span>
        {FILL_COLORS.map((color, index) => (
          <i key={color} style={{ background: color }} title={`${index + 1}`}></i>
        ))}
        <span class="vpp-graph-palette-note">YlOrRd</span>
      </div>
    </div>
  );
}
