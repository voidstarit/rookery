import assert from "node:assert/strict";
import test from "node:test";

import type { VNF } from "../src/inventory/types";
import {
  createPayload,
  updatePayload,
  type FormState,
} from "../src/inventory/vnfs/payload.ts";

/** baseForm returns a complete DHCPv4 form with no DNS integration. */
function baseForm(type: FormState["type"] = "dhcpv4"): FormState {
  return {
    type,
    name: "lab",
    networkID: 1,
    enabled: true,
    dhcp: {
      gateway: "10.20.0.1/24",
      rangeStart: "10.20.0.100",
      rangeEnd: "10.20.0.200",
      leaseTime: "12h",
      dns: "10.20.0.2",
      bindIP: "",
      transport: "",
      dnsVNFID: 0,
      dnsDomain: "",
      dnsTTL: "300",
    },
    dns: { forward: false },
    nat44: {
      gateway: "10.30.0.1/24",
      outsideInterface: "eth0",
      outsideAddress: "",
      outsideGateway: "",
      nat44Pool: "",
    },
    bgp: {
      transport: "memif",
      localAS: "65000",
      routerID: "10.40.0.1",
      peerAS: "65001",
      peerIP: "10.40.0.2",
      localIP: "10.40.0.1/24",
      peerGateway: "",
      multihopTTL: "64",
      password: "",
      passive: false,
      targetTableID: "0",
      nextHop: "",
    },
  };
}

/** storedDHCP is a protobuf-JSON response shape: zero and empty scalars are omitted. */
function storedDHCP(): VNF {
  return {
    id: 7,
    name: "lab",
    network_id: 1,
    network_name: "lab",
    type: "dhcpv4",
    family: "dhcpv4",
    enabled: true,
    gateway: "10.20.0.1/24",
    range_start: "10.20.0.100",
    range_end: "10.20.0.200",
    lease_time: "12h",
    dns: "10.20.0.2",
    status: "running",
  } as unknown as VNF;
}

test("create omits DNS integration defaults when none is selected", () => {
  const payload = createPayload(baseForm());
  assert.equal(payload.dns_vnf_id, 0);
  assert.equal(payload.dns_domain, "");
  assert.equal(payload.dns_ttl, 0);
});

test("create carries the DNS domain and TTL when integration is selected", () => {
  const form = baseForm();
  form.dhcp.dnsVNFID = 5;
  form.dhcp.dnsDomain = "lab.test";
  form.dhcp.dnsTTL = "600";
  const payload = createPayload(form);
  assert.equal(payload.dns_vnf_id, 5);
  assert.equal(payload.dns_domain, "lab.test");
  assert.equal(payload.dns_ttl, 600);
});

test("an unchanged parent produces no update payload", () => {
  const payload = updatePayload(baseForm(), storedDHCP());
  assert.deepEqual(payload, {});
});

test("changing one scalar sends only that field", () => {
  const form = baseForm();
  form.dhcp.rangeEnd = "10.20.0.250";
  const payload = updatePayload(form, storedDHCP());
  assert.deepEqual(payload, { range_end: "10.20.0.250" });
});

test("clearing DNS integration sends the zeroed group", () => {
  const stored = {
    ...storedDHCP(),
    dns_vnf_id: 5,
    dns_domain: "lab.test",
    dns_ttl: 300,
  } as unknown as VNF;
  const payload = updatePayload(baseForm(), stored);
  assert.equal(payload.dns_vnf_id, 0);
  assert.equal(payload.dns_domain, "");
  assert.equal(payload.dns_ttl, 0);
});

test("BGP false and empty scalars omitted by protojson do not trigger a restart", () => {
  const form = baseForm("bgp");
  form.name = "bgp";
  const stored = {
    id: 8,
    name: "bgp",
    network_id: 1,
    network_name: "lab",
    type: "bgp",
    enabled: true,
    transport: "memif",
    local_as: 65000,
    router_id: "10.40.0.1",
    peer_as: 65001,
    peer_ip: "10.40.0.2",
    local_ip: "10.40.0.1/24",
    multihop_ttl: 64,
  } as unknown as VNF;
  assert.deepEqual(updatePayload(form, stored), {});
});
