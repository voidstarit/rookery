import { useEffect, useMemo, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { TraceResult } from "../types";

type PacketTracePanelProps = {
  active: boolean;
};

type TracePacket = {
  number: number;
  nodes: string[];
  raw: string;
};

const tracePacketPattern = /^\s*Packet\s+(\d+)\s*$/;
const traceNodePattern = /^\s*\d{2}:\d{2}:\d{2}:\d+:\s+(\S+)/;

function parseTracePackets(raw: string): TracePacket[] {
  const lines = raw.replace(/\r/g, "").split("\n");
  const packets: TracePacket[] = [];
  let current: { number: number; nodes: string[]; lines: string[] } | null = null;

  for (const line of lines) {
    const packetMatch = line.match(tracePacketPattern);
    if (packetMatch) {
      if (current) {
        packets.push({ ...current, raw: current.lines.join("\n").trim() });
      }
      current = { number: Number(packetMatch[1]), nodes: [], lines: [line] };
      continue;
    }
    if (!current) {
      continue;
    }
    current.lines.push(line);
    const nodeMatch = line.match(traceNodePattern);
    if (nodeMatch) {
      current.nodes.push(nodeMatch[1]);
    }
  }
  if (current) {
    packets.push({ ...current, raw: current.lines.join("\n").trim() });
  }

  if (packets.length === 0) {
    const nodes = lines
      .map((line) => line.match(traceNodePattern)?.[1])
      .filter((node): node is string => Boolean(node));
    if (nodes.length > 0) {
      packets.push({ number: 1, nodes, raw: raw.trim() });
    }
  }
  return packets;
}

function TracePathGraph({ packet }: { packet: TracePacket }) {
  if (packet.nodes.length === 0) {
    return <p class="empty-state">No VPP node path found for this packet.</p>;
  }
  return (
    <div class="trace-path-panel">
      <div class="trace-path-head">
        <strong>Packet {packet.number}</strong>
        <span class="muted">{packet.nodes.length} nodes</span>
      </div>
      <ol class="trace-path" aria-label={`Packet ${packet.number} node path`}>
        {packet.nodes.map((nodeName, index) => (
          <li key={`${nodeName}-${index}`}>
            <span class="trace-path-node" data-drop={nodeName.includes("drop") ? "true" : undefined}>
              {nodeName}
            </span>
            {index < packet.nodes.length - 1 ? (
              <span class="trace-path-arrow" aria-hidden="true">
                ↓
              </span>
            ) : null}
          </li>
        ))}
      </ol>
    </div>
  );
}

export function PacketTracePanel({ active }: PacketTracePanelProps) {
  const [node, setNode] = useState("");
  const [count, setCount] = useState("100");
  const [result, setResult] = useState<TraceResult | null>(null);
  const [starting, setStarting] = useState(false);
  const [loading, setLoading] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [error, setError] = useState("");
  const [nodes, setNodes] = useState<string[]>([]);
  const [nodesLoading, setNodesLoading] = useState(false);
  const [nodesError, setNodesError] = useState("");
  const [selectedPacket, setSelectedPacket] = useState(0);

  const refreshNodes = async () => {
    setNodesLoading(true);
    try {
      const data = await api<string[] | { nodes?: string[] }>("/api/troubleshoot/trace/nodes");
      const next = Array.isArray(data) ? data : Array.isArray(data.nodes) ? data.nodes : [];
      setNodes(next);
      setNodesError("");
    } catch (err) {
      setNodesError(err instanceof Error ? err.message : String(err));
    } finally {
      setNodesLoading(false);
    }
  };

  useEffect(() => {
    if (!active) {
      return;
    }
    void refreshNodes();
    // Load the full VPP node list when the Troubleshoot section becomes active.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  useEffect(() => {
    if (node || nodes.length === 0) {
      return;
    }
    setNode(nodes[0]);
  }, [node, nodes]);


  const startTrace = async (event: Event) => {
    event.preventDefault();
    if (starting || !node.trim()) {
      return;
    }
    setError("");
    setStarting(true);
    setResult(null);
    try {
      const response = await api<TraceResult>("/api/troubleshoot/trace", {
        method: "POST",
        body: JSON.stringify({ node: node.trim(), count: Number(count) }),
      });
      setResult({ ...response, raw: "" });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  };

  const showTrace = async () => {
    setError("");
    setLoading(true);
    try {
      const response = await api<TraceResult>("/api/troubleshoot/trace");
      setResult((current) => ({
        ...response,
        node: current?.node ?? node,
        count: current?.count ?? Number(count),
      }));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  const packets = useMemo(() => parseTracePackets(result?.raw ?? ""), [result?.raw]);

  useEffect(() => {
    setSelectedPacket(0);
  }, [result?.raw]);

  const activePacket = packets[selectedPacket];

  const clearTrace = async () => {
    setError("");
    setClearing(true);
    try {
      await api("/api/troubleshoot/trace", { method: "DELETE" });
      setResult(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setClearing(false);
    }
  };

  return (
    <div class="trace-panel">
      <form class="trace-form" novalidate onSubmit={(event) => void startTrace(event)}>
        <div class="field trace-node-field">
          <span class="field-label">VPP graph node</span>
          <select
            class="trace-node-list"
            required
            value={node}
            onChange={(event) => setNode(event.currentTarget.value)}
          >
            <option value="" disabled>
              Select a VPP node…
            </option>
            {nodes.map((name) => (
              <option value={name} key={name}>
                {name}
              </option>
            ))}
          </select>
          <span class="field-hint">
            {nodes.length > 0
              ? `${nodes.length} VPP node${nodes.length === 1 ? "" : "s"} available.`
              : nodesError
                ? nodesError
                : "No VPP nodes loaded yet."}
          </span>
          <button class="row-action trace-refresh-nodes" type="button" disabled={nodesLoading} onClick={() => void refreshNodes()}>
            {nodesLoading ? "Loading nodes…" : "Refresh nodes"}
          </button>
        </div>
        <label class="field trace-count-field">
          <span class="field-label">Packet count</span>
          <input
            type="number"
            required
            min={1}
            max={1000}
            value={count}
            onInput={(event) => setCount(event.currentTarget.value)}
          />
          <span class="field-hint">Trace up to 1,000 packets.</span>
        </label>
        <button
          class="button primary"
          type="submit"
          disabled={starting || !node.trim() || Number(count) < 1 || Number(count) > 1000}
        >
          {starting ? "Starting…" : "Start trace"}
        </button>
        <button class="button ghost" type="button" disabled={loading} onClick={() => void showTrace()}>
          {loading ? "Loading…" : "Show trace"}
        </button>
        <button class="button ghost" type="button" disabled={clearing} onClick={() => void clearTrace()}>
          {clearing ? "Clearing…" : "Clear"}
        </button>
      </form>

      <p class="field-hint">
        Start a trace, generate traffic through the selected node, then click <strong>Show trace</strong>.
      </p>

      {error ? (
        <div class="banner" role="alert">
          {error}
        </div>
      ) : null}

      {result ? (
        <div class="trace-result">
          <div class="trace-result-head">
            <span>{result.node ? `${result.node} · ${result.count || 0} packets` : "VPP trace"}</span>
            {result.captured_at ? <span class="muted">{new Date(result.captured_at).toLocaleString()}</span> : null}
          </div>

          {packets.length > 0 ? (
            <div class="trace-visual">
              <label class="field trace-packet-field">
                <span class="field-label">Packet</span>
                <select
                  value={String(selectedPacket)}
                  onChange={(event) => setSelectedPacket(Number(event.currentTarget.value))}
                >
                  {packets.map((packet, index) => (
                    <option value={String(index)} key={`${packet.number}-${index}`}>
                      Packet {packet.number} · {packet.nodes.length} nodes
                    </option>
                  ))}
                </select>
                <span class="field-hint">Select a packet to draw its VPP node path.</span>
              </label>
              {activePacket ? <TracePathGraph packet={activePacket} /> : null}
            </div>
          ) : (
            <p class="empty-state">No trace packets captured yet. Generate traffic and click Show trace.</p>
          )}

          <details class="trace-raw">
            <summary>Raw trace text</summary>
            <pre class="trace-output">{result.raw || "No trace packets captured yet. Generate traffic and click Show trace."}</pre>
          </details>
        </div>
      ) : (
        <p class="empty-state">No trace loaded.</p>
      )}
    </div>
  );
}
