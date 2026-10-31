import { h, render } from "preact";
import { mountToastHost } from "../shared/toast";
import { ClientCertificates } from "../settings/ClientCertificates";
import { startInventory } from "./app";
import { mountDisksTab } from "./disks/main";
import { mountLightVMTab } from "./lightvm/main";
import { mountMediaTab } from "./media/main";
import { mountNetworksTab } from "./networks/main";
import { mountSettingsTab } from "./settings/main";
import { mountVNFsTab } from "./vnfs/main";
import { mountVMsTab } from "./vms/main";

const toastRoot = document.getElementById("toast-root");
if (toastRoot) {
  mountToastHost(toastRoot);
}

mountVMsTab(document.getElementById("vms-root"));
mountDisksTab(document.getElementById("disks-root"));
mountMediaTab(document.getElementById("media-root"));
mountLightVMTab(document.getElementById("lightvm-root"));
mountNetworksTab(document.getElementById("networks-root"));
mountVNFsTab(document.getElementById("vnfs-root"));
mountSettingsTab(document.getElementById("settings-root"));

const clientCertsRoot = document.getElementById("client-certs-root");
if (clientCertsRoot) {
  render(h(ClientCertificates, {}), clientCertsRoot);
}

startInventory();
