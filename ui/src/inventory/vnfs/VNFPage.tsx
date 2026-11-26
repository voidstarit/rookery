import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { LabelsInput, saveEntityLabelsWithToast, useEntityLabelDraft } from "../../shared/labels";
import type {
  BGPPrefix,
  BGPVNFRecord,
  DHCPOption,
  DHCPStaticAssignment,
  DHCPVNFRecord,
  DNATRule,
  DNSForwarder,
  DNSRecord,
  DNSRecordType,
  DNSVNFRecord,
  NAT44VNFRecord,
  Network,
  VNF,
  VNFType,
} from "../types";
import { BGPAdvertisedPrefixesEditor } from "./BGPAdvertisedPrefixesEditor";
import { BGPVNFForm, defaultBGPVNFForm, type BGPVNFFormState } from "./BGPVNFForm";
import { DHCPOptionsEditor } from "./DHCPOptionsEditor";
import { DHCPStaticAssignmentsEditor } from "./DHCPStaticAssignmentsEditor";
import { DHCPVNFForm, defaultDHCPVNFForm, type DHCPVNFFormState } from "./DHCPVNFForm";
import { DNATRulesEditor } from "./DNATRulesEditor";
import { DNSForwardersEditor } from "./DNSForwardersEditor";
import { DNSRecordsEditor } from "./DNSRecordsEditor";
import { DNSVNFForm, defaultDNSVNFForm, type DNSVNFFormState } from "./DNSVNFForm";
import { NAT44VNFForm, defaultNAT44VNFForm, type NAT44VNFFormState } from "./NAT44VNFForm";
import {
  firstIPv4Host,
  tagBGPVNF,
  tagDHCPVNF,
  tagDNSVNF,
  tagNAT44VNF,
  vnfAPISlug,
} from "./format";

type VNFPageProps = {
  vnf: VNF | null;
  networks: Network[];
  vppReady: boolean;
  initialNetworkID?: number;
  onClose: () => void;
  onSaved: (vnf: VNF) => void;
};

type FormState = {
  type: VNFType;
  name: string;
  networkID: number;
  enabled: boolean;
  dhcp: DHCPVNFFormState;
  dns: DNSVNFFormState;
  nat44: NAT44VNFFormState;
  bgp: BGPVNFFormState;
};

type VNFRecord = DHCPVNFRecord | DNSVNFRecord | NAT44VNFRecord | BGPVNFRecord;

type ChildRows = {
  staticAssignments: DHCPStaticAssignment[];
  options: DHCPOption[];
  records: DNSRecord[];
  forwarders: DNSForwarder[];
  dnatRules: DNATRule[];
  prefixes: BGPPrefix[];
};

type ChildOperation = {
  description: string;
  run: () => Promise<unknown>;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

const EMPTY_CHILDREN: ChildRows = {
  staticAssignments: [],
  options: [],
  records: [],
  forwarders: [],
  dnatRules: [],
  prefixes: [],
};

/**
 * typeHint returns a short explanation of the selected VNF type, summarising its protocol,
 * transport, and addressing model, with DHCPv4 as the fallback.
 */
function typeHint(type: VNFType): string {
  if (type === "dns") {
    return "Local DNS server on the network service address (.2) over IPv4 and IPv6. Serve A/AAAA/CNAME/TXT/PTR records and optionally forward other names upstream.";
  }
  if (type === "bgp") {
    return "Embedded IPv4/IPv6 BGP speaker. Select memif for userspace/gVisor or tap for kernel TCP with TCP MD5 support.";
  }
  if (type === "nat44") {
    return "IPv4 NAT44 gateway. A dedicated network BVI owns the gateway; the outside NIC is taken over by VPP.";
  }
  if (type === "dhcpv6") {
    return "IPv6 address pool with stateful IA_NA leases on a VPP tap (shared memif is DHCPv4-only for now).";
  }
  return "IPv4 address pool served on the network-shared memif by default (VNF address .2).";
}

/**
 * defaultForm builds the initial or edit shell state, selecting the requested network and deriving
 * per-type defaults without touching a stored VNF.
 */
function defaultForm(
  vnf: VNF | null,
  networks: Network[],
  vppReady: boolean,
  initialNetworkID?: number
): FormState {
  const type = vnf?.type ?? "dhcpv4";
  const networkID = vnf?.network_id ?? initialNetworkID ?? networks[0]?.id ?? 0;
  const network = networks.find((item) => item.id === networkID);
  const family = type === "dhcpv6" ? "dhcpv6" : "dhcpv4";
  return {
    type,
    name: vnf?.name ?? "",
    networkID,
    enabled: vnf ? vnf.enabled : vppReady,
    dhcp: defaultDHCPVNFForm(
      vnf && (vnf.type === "dhcpv4" || vnf.type === "dhcpv6") ? vnf : null,
      network,
      family
    ),
    dns: defaultDNSVNFForm(vnf?.type === "dns" ? vnf : null),
    nat44: defaultNAT44VNFForm(vnf?.type === "nat44" ? vnf : null, network),
    bgp: defaultBGPVNFForm(vnf?.type === "bgp" ? vnf : null),
  };
}

/**
 * tagRecord applies the selected type tag to one parent API response so the saved record can join
 * the merged inventory union.
 */
function tagRecord(type: VNFType, record: VNFRecord): VNF {
  switch (type) {
    case "dhcpv4":
    case "dhcpv6":
      return tagDHCPVNF(record as DHCPVNFRecord);
    case "dns":
      return tagDNSVNF(record as DNSVNFRecord);
    case "nat44":
      return tagNAT44VNF(record as NAT44VNFRecord);
    case "bgp":
      return tagBGPVNF(record as BGPVNFRecord);
  }
}

/**
 * scalarPayload builds the type-specific parent scalar fields shared by create and update; it never
 * includes child arrays, identity, or the enabled transition.
 */
function scalarPayload(form: FormState): Record<string, unknown> {
  switch (form.type) {
    case "dhcpv4":
    case "dhcpv6":
      return {
        gateway: form.dhcp.gateway.trim(),
        range_start: form.dhcp.rangeStart.trim(),
        range_end: form.dhcp.rangeEnd.trim(),
        lease_time: form.dhcp.leaseTime.trim() || "12h",
        dns: form.dhcp.dns.trim(),
        bind_ip: form.dhcp.bindIP.trim(),
        transport: form.dhcp.transport,
        dns_vnf_id: form.dhcp.dnsVNFID,
        dns_domain: form.dhcp.dnsDomain.trim(),
        dns_ttl: Number(form.dhcp.dnsTTL) || 300,
      };
    case "dns":
      return { forward: form.dns.forward };
    case "nat44":
      return {
        gateway: form.nat44.gateway.trim(),
        outside_interface: form.nat44.outsideInterface,
        outside_address: form.nat44.outsideAddress.trim(),
        outside_gateway: form.nat44.outsideGateway.trim(),
        nat44_pool: form.nat44.nat44Pool.trim(),
      };
    case "bgp":
      return {
        transport: form.bgp.transport,
        local_as: Number(form.bgp.localAS),
        router_id: form.bgp.routerID.trim(),
        peer_as: Number(form.bgp.peerAS),
        peer_ip: form.bgp.peerIP.trim(),
        local_ip: form.bgp.localIP.trim(),
        peer_gateway: form.bgp.peerGateway.trim(),
        multihop_ttl: Number(form.bgp.multihopTTL) || 64,
        passive: form.bgp.passive,
        target_table_id: Number(form.bgp.targetTableID) || 0,
        next_hop: form.bgp.nextHop.trim(),
        ...(form.bgp.transport === "tap" && form.bgp.password ? { password: form.bgp.password } : {}),
      };
  }
}

/**
 * createPayload wraps the parent scalars with identity and always creates the VNF disabled so the
 * child rows can be written before the single enabling start.
 */
function createPayload(form: FormState): Record<string, unknown> {
  const payload: Record<string, unknown> = {
    name: form.name.trim(),
    network_id: form.networkID,
    enabled: false,
    ...scalarPayload(form),
  };
  if (form.type === "dhcpv4" || form.type === "dhcpv6") {
    payload.family = form.type;
  }
  return payload;
}

/**
 * updatePayload returns only the parent scalar fields that differ from the stored record; an
 * unchanged parent is skipped so it does not stop and start the runtime, and enabled is applied
 * separately after the child writes.
 */
function updatePayload(form: FormState, base: VNF | null): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  const name = form.name.trim();
  if (!base || name !== base.name) {
    payload.name = name;
  }
  if (!base || base.type !== form.type) {
    return { ...payload, ...scalarPayload(form) };
  }
  const stored = base as unknown as Record<string, unknown>;
  for (const [key, value] of Object.entries(scalarPayload(form))) {
    if (stored[key] !== value) {
      payload[key] = value;
    }
  }
  return payload;
}

/**
 * diffRows splits child drafts into the delete, update, and create operations of the per-item save
 * order, comparing rows by their server ID and natural-key signature.
 */
function diffRows<T extends { id?: number }>(
  original: T[],
  drafts: T[],
  signature: (row: T) => string
): { deleted: T[]; updated: T[]; created: T[] } {
  const originalByID = new Map<number, T>();
  for (const row of original) {
    if (row.id !== undefined) {
      originalByID.set(row.id, row);
    }
  }
  const draftIDs = new Set<number>();
  for (const row of drafts) {
    if (row.id !== undefined) {
      draftIDs.add(row.id);
    }
  }
  return {
    deleted: original.filter((row) => row.id !== undefined && !draftIDs.has(row.id)),
    updated: drafts.filter((row) => {
      if (row.id === undefined) {
        return false;
      }
      const previous = originalByID.get(row.id);
      return previous !== undefined && signature(previous) !== signature(row);
    }),
    created: drafts.filter((row) => row.id === undefined),
  };
}

const assignmentSignature = (row: DHCPStaticAssignment) => `${row.mac.trim().toLowerCase()}|${row.ip.trim()}`;
const optionSignature = (row: DHCPOption) => `${row.code}|${row.format}|${row.value.trim()}`;
const recordSignature = (row: DNSRecord) =>
  `${row.name.trim().toLowerCase()}|${row.type}|${row.value.trim()}|${Number(row.ttl) || 300}`;
const forwarderSignature = (row: DNSForwarder) =>
  `${row.address.trim().toLowerCase()}|${Number(row.position) || 0}`;
const dnatSignature = (row: DNATRule) =>
  `${row.protocol}|${Number(row.external_port)}|${row.local_ip.trim()}|${Number(row.local_port)}`;
const prefixSignature = (row: BGPPrefix) => row.prefix.trim().toLowerCase();

/**
 * VNFPage renders the typed create or edit shell: it chooses the VNF type, saves the parent scalar
 * fields through the type-specific endpoint, and applies child editors through per-item endpoints
 * in delete-then-update-then-create order. A failing child write reloads the child lists because the
 * server has already restored the previous row and runtime.
 */
export function VNFPage({ vnf, networks, vppReady, initialNetworkID, onClose, onSaved }: VNFPageProps) {
  const [form, setForm] = useState<FormState>(() => defaultForm(vnf, networks, vppReady, initialNetworkID));
  const [createdVNF, setCreatedVNF] = useState<VNF | null>(null);
  const [staticAssignments, setStaticAssignments] = useState<DHCPStaticAssignment[]>([]);
  const [dhcpOptions, setDHCPOptions] = useState<DHCPOption[]>([]);
  const [dnsRecords, setDNSRecords] = useState<DNSRecord[]>([]);
  const [dnsForwarders, setDNSForwarders] = useState<DNSForwarder[]>([]);
  const [dnatRules, setDNATRules] = useState<DNATRule[]>([]);
  const [advertisedPrefixes, setAdvertisedPrefixes] = useState<BGPPrefix[]>([]);
  const [childrenLoading, setChildrenLoading] = useState(vnf !== null);
  const [childrenError, setChildrenError] = useState("");
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState("");
  const [labels, setLabels] = useEntityLabelDraft("vnf", vnf?.id);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);
  const originals = useRef<ChildRows>({ ...EMPTY_CHILDREN });

  const editing = vnf !== null;
  const selectedNetwork = networks.find((item) => item.id === form.networkID);
  const family = form.type === "dhcpv6" ? "dhcpv6" : "dhcpv4";

  useEffect(() => {
    nameRef.current?.focus();
    document.title = `${vnf ? "Edit" : "Add"} VNF · rookery`;
  }, [vnf]);

  const resetChildren = () => {
    originals.current = { ...EMPTY_CHILDREN };
    setStaticAssignments([]);
    setDHCPOptions([]);
    setDNSRecords([]);
    setDNSForwarders([]);
    setDNATRules([]);
    setAdvertisedPrefixes([]);
    setChildrenError("");
  };

  const loadChildren = async (type: VNFType, id: number) => {
    setChildrenLoading(true);
    setChildrenError("");
    try {
      if (type === "dhcpv4" || type === "dhcpv6") {
        const [options, assignments] = await Promise.all([
          api<DHCPOption[]>(`/api/dhcp-vnfs/${id}/options`),
          type === "dhcpv4"
            ? api<DHCPStaticAssignment[]>(`/api/dhcp-vnfs/${id}/static-assignments`)
            : Promise.resolve<DHCPStaticAssignment[]>([]),
        ]);
        const optionRows = Array.isArray(options) ? options : [];
        const assignmentRows = Array.isArray(assignments) ? assignments : [];
        setDHCPOptions(optionRows);
        setStaticAssignments(assignmentRows);
        originals.current.options = optionRows;
        originals.current.staticAssignments = assignmentRows;
      } else if (type === "dns") {
        const [records, forwarders] = await Promise.all([
          api<DNSRecord[]>(`/api/dns-vnfs/${id}/records`),
          api<DNSForwarder[]>(`/api/dns-vnfs/${id}/forwarders`),
        ]);
        const recordRows = Array.isArray(records) ? records : [];
        const forwarderRows = Array.isArray(forwarders) ? forwarders : [];
        setDNSRecords(recordRows);
        setDNSForwarders(forwarderRows);
        originals.current.records = recordRows;
        originals.current.forwarders = forwarderRows;
      } else if (type === "nat44") {
        const rules = await api<DNATRule[]>(`/api/nat44-vnfs/${id}/dnat-rules`);
        const ruleRows = Array.isArray(rules) ? rules : [];
        setDNATRules(ruleRows);
        originals.current.dnatRules = ruleRows;
      } else {
        const prefixes = await api<BGPPrefix[]>(`/api/bgp-vnfs/${id}/advertised-prefixes`);
        const prefixRows = Array.isArray(prefixes) ? prefixes : [];
        setAdvertisedPrefixes(prefixRows);
        originals.current.prefixes = prefixRows;
      }
    } catch (err) {
      setChildrenError(
        `Could not load this VNF's child rows: ${err instanceof Error ? err.message : String(err)}`
      );
    } finally {
      setChildrenLoading(false);
    }
  };

  useEffect(() => {
    if (vnf) {
      void loadChildren(vnf.type, vnf.id);
    }
    // The VNF page is remounted for each edited record.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [vnf]);

  const update = <K extends keyof FormState>(key: K, value: FormState[K]) => {
    setForm((current) => ({ ...current, [key]: value }));
  };

  const changeNetwork = (networkID: number) => {
    const network = networks.find((item) => item.id === networkID);
    const gateway = firstIPv4Host(network?.ipv4_cidr);
    setForm((current) => ({
      ...current,
      networkID,
      dhcp: {
        ...current.dhcp,
        ...(current.type === "dhcpv4" && gateway ? { gateway } : {}),
        dnsVNFID: 0,
      },
      nat44: {
        ...current.nat44,
        ...(gateway ? { gateway } : {}),
      },
    }));
  };

  const changeType = (type: VNFType) => {
    if (editing) {
      return;
    }
    setForm((current) => {
      const network = networks.find((item) => item.id === current.networkID);
      return {
        ...current,
        type,
        dhcp: defaultDHCPVNFForm(null, network, type === "dhcpv6" ? "dhcpv6" : "dhcpv4"),
        dns: defaultDNSVNFForm(null),
        nat44: defaultNAT44VNFForm(null, network),
        bgp: defaultBGPVNFForm(null),
      };
    });
    resetChildren();
  };

  const close = () => {
    if (!saving) {
      onClose();
    }
  };

  const validateChildren = (): string => {
    if (form.type === "dhcpv4") {
      const macs = new Set<string>();
      const ips = new Set<string>();
      for (const row of staticAssignments) {
        const mac = row.mac.trim().toLowerCase();
        const ip = row.ip.trim();
        if (!mac || !ip) {
          continue;
        }
        if (macs.has(mac)) {
          return `Duplicate static assignment MAC ${row.mac}.`;
        }
        if (ips.has(ip)) {
          return `Duplicate static assignment IP ${row.ip}.`;
        }
        macs.add(mac);
        ips.add(ip);
      }
    }
    if (form.type === "dhcpv4" || form.type === "dhcpv6") {
      const codes = new Set<number>();
      for (const row of dhcpOptions) {
        if (codes.has(row.code)) {
          return `Duplicate DHCP option code ${row.code}.`;
        }
        codes.add(row.code);
      }
    } else if (form.type === "dns") {
      const keys = new Set<string>();
      const nameTypes = new Map<string, Set<DNSRecordType>>();
      const cnameNames = new Set<string>();
      for (const row of dnsRecords) {
        const name = row.name.trim().toLowerCase();
        const key = `${name}|${row.type}|${row.value.trim().toLowerCase()}`;
        if (keys.has(key)) {
          return `Duplicate DNS record ${row.name} ${row.type} ${row.value}.`;
        }
        keys.add(key);
        if (row.type === "CNAME") {
          if (cnameNames.has(name)) {
            return `Name ${row.name} has more than one CNAME record.`;
          }
          cnameNames.add(name);
        }
        const types = nameTypes.get(name) ?? new Set<DNSRecordType>();
        types.add(row.type);
        nameTypes.set(name, types);
      }
      for (const [name, types] of nameTypes) {
        if (types.has("CNAME") && types.size > 1) {
          return `CNAME ${name} cannot coexist with other record types for the same name.`;
        }
      }
      const addresses = new Set<string>();
      for (const row of dnsForwarders) {
        const address = row.address.trim().toLowerCase();
        if (!address) {
          continue;
        }
        if (addresses.has(address)) {
          return `Duplicate DNS forwarder ${row.address}.`;
        }
        addresses.add(address);
      }
    } else if (form.type === "nat44") {
      const keys = new Set<string>();
      for (const row of dnatRules) {
        const key = `${row.protocol}|${Number(row.external_port)}`;
        if (keys.has(key)) {
          return `Duplicate DNAT ${row.protocol} external port ${row.external_port}.`;
        }
        keys.add(key);
      }
    } else {
      const prefixes = new Set<string>();
      for (const row of advertisedPrefixes) {
        const prefix = row.prefix.trim().toLowerCase();
        if (!prefix) {
          continue;
        }
        if (prefixes.has(prefix)) {
          return `Duplicate advertised prefix ${row.prefix}.`;
        }
        prefixes.add(prefix);
      }
    }
    return "";
  };

  const saveChildren = async (type: VNFType, id: number): Promise<boolean> => {
    const deletes: ChildOperation[] = [];
    const updates: ChildOperation[] = [];
    const creates: ChildOperation[] = [];

    if (type === "dhcpv4") {
      const base = `/api/dhcp-vnfs/${id}/static-assignments`;
      const diff = diffRows(originals.current.staticAssignments, staticAssignments, assignmentSignature);
      for (const row of diff.deleted) {
        deletes.push({
          description: `static assignment ${row.mac}`,
          run: () => api(`${base}/${row.id}`, { method: "DELETE" }),
        });
      }
      for (const row of diff.updated) {
        updates.push({
          description: `static assignment ${row.mac}`,
          run: () =>
            api(`${base}/${row.id}`, {
              method: "PUT",
              body: JSON.stringify({ mac: row.mac.trim(), ip: row.ip.trim() }),
            }),
        });
      }
      for (const row of diff.created) {
        creates.push({
          description: `static assignment ${row.mac}`,
          run: () =>
            api(base, {
              method: "POST",
              body: JSON.stringify({ mac: row.mac.trim(), ip: row.ip.trim() }),
            }),
        });
      }
    }

    if (type === "dhcpv4" || type === "dhcpv6") {
      const base = `/api/dhcp-vnfs/${id}/options`;
      const diff = diffRows(originals.current.options, dhcpOptions, optionSignature);
      for (const row of diff.deleted) {
        deletes.push({
          description: `DHCP option ${row.code}`,
          run: () => api(`${base}/${row.id}`, { method: "DELETE" }),
        });
      }
      for (const row of diff.updated) {
        updates.push({
          description: `DHCP option ${row.code}`,
          run: () =>
            api(`${base}/${row.id}`, {
              method: "PUT",
              body: JSON.stringify({ code: Number(row.code), format: row.format, value: row.value.trim() }),
            }),
        });
      }
      for (const row of diff.created) {
        creates.push({
          description: `DHCP option ${row.code}`,
          run: () =>
            api(base, {
              method: "POST",
              body: JSON.stringify({ code: Number(row.code), format: row.format, value: row.value.trim() }),
            }),
        });
      }
    }

    if (type === "dns") {
      const recordsBase = `/api/dns-vnfs/${id}/records`;
      const recordDiff = diffRows(originals.current.records, dnsRecords, recordSignature);
      for (const row of recordDiff.deleted) {
        deletes.push({
          description: `DNS record ${row.name}`,
          run: () => api(`${recordsBase}/${row.id}`, { method: "DELETE" }),
        });
      }
      for (const row of recordDiff.updated) {
        updates.push({
          description: `DNS record ${row.name}`,
          run: () =>
            api(`${recordsBase}/${row.id}`, {
              method: "PUT",
              body: JSON.stringify({
                name: row.name.trim(),
                type: row.type,
                value: row.value.trim(),
                ttl: Math.max(1, Number(row.ttl) || 300),
              }),
            }),
        });
      }
      for (const row of recordDiff.created) {
        creates.push({
          description: `DNS record ${row.name}`,
          run: () =>
            api(recordsBase, {
              method: "POST",
              body: JSON.stringify({
                name: row.name.trim(),
                type: row.type,
                value: row.value.trim(),
                ttl: Math.max(1, Number(row.ttl) || 300),
              }),
            }),
        });
      }

      const forwardersBase = `/api/dns-vnfs/${id}/forwarders`;
      const forwarderDiff = diffRows(originals.current.forwarders, dnsForwarders, forwarderSignature);
      for (const row of forwarderDiff.deleted) {
        deletes.push({
          description: `DNS forwarder ${row.address}`,
          run: () => api(`${forwardersBase}/${row.id}`, { method: "DELETE" }),
        });
      }
      for (const row of forwarderDiff.updated) {
        updates.push({
          description: `DNS forwarder ${row.address}`,
          run: () =>
            api(`${forwardersBase}/${row.id}`, {
              method: "PUT",
              body: JSON.stringify({ address: row.address.trim(), position: Number(row.position) || 0 }),
            }),
        });
      }
      for (const row of forwarderDiff.created) {
        creates.push({
          description: `DNS forwarder ${row.address}`,
          run: () =>
            api(forwardersBase, {
              method: "POST",
              body: JSON.stringify({ address: row.address.trim(), position: Number(row.position) || 0 }),
            }),
        });
      }
    }

    if (type === "nat44") {
      const base = `/api/nat44-vnfs/${id}/dnat-rules`;
      const diff = diffRows(originals.current.dnatRules, dnatRules, dnatSignature);
      for (const row of diff.deleted) {
        deletes.push({
          description: `DNAT rule ${row.protocol}/${row.external_port}`,
          run: () => api(`${base}/${row.id}`, { method: "DELETE" }),
        });
      }
      for (const row of diff.updated) {
        updates.push({
          description: `DNAT rule ${row.protocol}/${row.external_port}`,
          run: () =>
            api(`${base}/${row.id}`, {
              method: "PUT",
              body: JSON.stringify({
                protocol: row.protocol,
                external_port: Number(row.external_port),
                local_ip: row.local_ip.trim(),
                local_port: Number(row.local_port),
              }),
            }),
        });
      }
      for (const row of diff.created) {
        creates.push({
          description: `DNAT rule ${row.protocol}/${row.external_port}`,
          run: () =>
            api(base, {
              method: "POST",
              body: JSON.stringify({
                protocol: row.protocol,
                external_port: Number(row.external_port),
                local_ip: row.local_ip.trim(),
                local_port: Number(row.local_port),
              }),
            }),
        });
      }
    }

    if (type === "bgp") {
      const base = `/api/bgp-vnfs/${id}/advertised-prefixes`;
      const diff = diffRows(originals.current.prefixes, advertisedPrefixes, prefixSignature);
      for (const row of diff.deleted) {
        deletes.push({
          description: `advertised prefix ${row.prefix}`,
          run: () => api(`${base}/${row.id}`, { method: "DELETE" }),
        });
      }
      for (const row of diff.updated) {
        updates.push({
          description: `advertised prefix ${row.prefix}`,
          run: () =>
            api(`${base}/${row.id}`, {
              method: "PUT",
              body: JSON.stringify({ prefix: row.prefix.trim() }),
            }),
        });
      }
      for (const row of diff.created) {
        creates.push({
          description: `advertised prefix ${row.prefix}`,
          run: () =>
            api(base, {
              method: "POST",
              body: JSON.stringify({ prefix: row.prefix.trim() }),
            }),
        });
      }
    }

    let current = "";
    try {
      for (const operation of [...deletes, ...updates, ...creates]) {
        current = operation.description;
        await operation.run();
      }
      return true;
    } catch (err) {
      setFormError(
        `Could not save ${current}: ${err instanceof Error ? err.message : String(err)}. The previous configuration was restored.`
      );
      return false;
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || childrenLoading || !formRef.current?.reportValidity()) {
      return;
    }
    setFormError("");
    if (!form.name.trim() || !form.networkID) {
      setFormError("Name and network are required.");
      return;
    }
    const childError = validateChildren();
    if (childError) {
      setFormError(childError);
      return;
    }
    if (
      form.type === "nat44" &&
      form.enabled &&
      !window.confirm(
        `Take over ${form.nat44.outsideInterface} for NAT44?\n\nVPP will own this NIC and its host IPv4/IPv6 addresses will be flushed. The host default-route interface is rejected by the server.`
      )
    ) {
      return;
    }

    setSaving(true);
    let parentID = vnf?.id ?? createdVNF?.id;
    let childPhase = false;
    const wasEnabled = vnf?.enabled ?? createdVNF?.enabled ?? false;
    try {
      let lastParent: VNF;
      if (parentID === undefined) {
        lastParent = await api<VNFRecord>(`/api/${vnfAPISlug(form.type)}`, {
          method: "POST",
          body: JSON.stringify(createPayload(form)),
        }).then((record) => tagRecord(form.type, record));
        parentID = lastParent.id;
        setCreatedVNF(lastParent);
      } else {
        const payload = updatePayload(form, vnf ?? createdVNF);
        if (Object.keys(payload).length === 0 && (vnf ?? createdVNF)) {
          lastParent = (vnf ?? createdVNF) as VNF;
        } else {
          lastParent = await api<VNFRecord>(`/api/${vnfAPISlug(form.type)}/${parentID}`, {
            method: "PUT",
            body: JSON.stringify(payload),
          }).then((record) => tagRecord(form.type, record));
        }
      }

      childPhase = true;
      const childrenSaved = await saveChildren(form.type, parentID);
      if (!childrenSaved) {
        await loadChildren(form.type, parentID);
        return;
      }

      if (form.enabled !== wasEnabled) {
        lastParent = await api<VNFRecord>(`/api/${vnfAPISlug(form.type)}/${parentID}`, {
          method: "PUT",
          body: JSON.stringify({ enabled: form.enabled }),
        }).then((record) => tagRecord(form.type, record));
      }

      await saveEntityLabelsWithToast("vnf", parentID, form.name.trim(), labels);
      onSaved(lastParent);
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err));
      if (parentID !== undefined && childPhase) {
        // A failure after child writes may follow successful operations; reload
        // so the next save diffs against the server rather than stale row IDs.
        await loadChildren(form.type, parentID);
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <div class="vnf-page">
      <form ref={formRef} class="vnf-page-form" novalidate onSubmit={(event) => void submit(event)}>
        <header class="route-page-head">
          <div>
            <button class="back-link" type="button" disabled={saving} onClick={close}>
              ← VNFs
            </button>
            <p class="eyebrow">Network function</p>
            <h2 id="vnf-dialog-title">{editing ? `Edit ${vnf?.name ?? "VNF"}` : "Add VNF"}</h2>
          </div>
        </header>

        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">Name</span>
            <input
              ref={nameRef}
              type="text"
              required
              maxlength={64}
              pattern={NAME_PATTERN}
              autocomplete="off"
              spellcheck={false}
              placeholder="lab-dhcpv4"
              value={form.name}
              onInput={(event) => update("name", event.currentTarget.value)}
            />
            <span class="field-hint">Letters, digits, dots, dashes and underscores.</span>
          </label>

          <label class="field">
            <span class="field-label">Network</span>
            <select
              required
              disabled={editing || saving}
              value={String(form.networkID)}
              onChange={(event) => changeNetwork(Number(event.currentTarget.value))}
            >
              {networks.map((network) => (
                <option value={String(network.id)} key={network.id}>
                  {network.name} · bd {network.bridge_domain_id}
                </option>
              ))}
            </select>
          </label>

          <label class="field">
            <span class="field-label">Type</span>
            <select
              required
              disabled={editing || saving}
              value={form.type}
              onChange={(event) => changeType(event.currentTarget.value as VNFType)}
            >
              <option value="dhcpv4">DHCPv4</option>
              <option value="dhcpv6">DHCPv6</option>
              <option value="dns">DNS</option>
              <option value="nat44">NAT44</option>
              <option value="bgp">BGP</option>
            </select>
            <span class="field-hint">{typeHint(form.type)}</span>
          </label>
        </div>

        <div class="form-grid">
          <div class="field field-wide">
            <LabelsInput labels={labels} onChange={setLabels} disabled={saving} />
          </div>
        </div>

        {form.type === "dhcpv4" || form.type === "dhcpv6" ? (
          <>
            <DHCPVNFForm
              state={form.dhcp}
              family={family}
              network={selectedNetwork}
              editing={editing}
              enabled={form.enabled}
              vppReady={vppReady}
              disabled={saving}
              onChange={(dhcp) => update("dhcp", dhcp)}
              onEnabledChange={(enabled) => update("enabled", enabled)}
            />
            {form.type === "dhcpv4" ? (
              <DHCPStaticAssignmentsEditor
                assignments={staticAssignments}
                disabled={saving}
                onChange={setStaticAssignments}
              />
            ) : null}
            <DHCPOptionsEditor
              options={dhcpOptions}
              family={family}
              disabled={saving}
              onChange={setDHCPOptions}
            />
          </>
        ) : form.type === "dns" ? (
          <>
            <DNSVNFForm
              state={form.dns}
              editing={editing}
              enabled={form.enabled}
              vppReady={vppReady}
              disabled={saving}
              onChange={(dns) => update("dns", dns)}
              onEnabledChange={(enabled) => update("enabled", enabled)}
            />
            <DNSRecordsEditor records={dnsRecords} disabled={saving} onChange={setDNSRecords} />
            <DNSForwardersEditor
              forwarders={dnsForwarders}
              disabled={saving}
              onChange={setDNSForwarders}
            />
          </>
        ) : form.type === "nat44" ? (
          <>
            <NAT44VNFForm
              state={form.nat44}
              network={selectedNetwork}
              editing={editing}
              enabled={form.enabled}
              vppReady={vppReady}
              disabled={saving}
              onChange={(nat44) => update("nat44", nat44)}
              onEnabledChange={(enabled) => update("enabled", enabled)}
            />
            <DNATRulesEditor rules={dnatRules} disabled={saving} onChange={setDNATRules} />
          </>
        ) : (
          <>
            <BGPVNFForm
              state={form.bgp}
              editing={editing}
              enabled={form.enabled}
              vppReady={vppReady}
              disabled={saving}
              onChange={(bgp) => update("bgp", bgp)}
              onEnabledChange={(enabled) => update("enabled", enabled)}
            />
            <BGPAdvertisedPrefixesEditor
              prefixes={advertisedPrefixes}
              disabled={saving}
              onChange={setAdvertisedPrefixes}
            />
          </>
        )}

        {childrenLoading ? <p class="field-hint">Loading child rows…</p> : null}
        {childrenError ? (
          <div class="banner" role="alert">
            {childrenError}
          </div>
        ) : null}

        {formError ? (
          <p class="form-error" role="alert">
            {formError}
          </p>
        ) : null}

        <footer class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving || childrenLoading}>
            {saving ? "Saving…" : editing ? "Save VNF" : "Add VNF"}
          </button>
        </footer>
      </form>
    </div>
  );
}
