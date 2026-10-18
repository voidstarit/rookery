import { mountToastHost } from "../shared/toast";
import { startInventory } from "./app";
import { mountDisksTab } from "./disks/main";
import { mountMediaTab } from "./media/main";
import { mountNetworksTab } from "./networks/main";
import { mountVNFsTab } from "./vnfs/main";

const toastRoot = document.getElementById("toast-root");
if (toastRoot) {
  mountToastHost(toastRoot);
}

mountDisksTab(document.getElementById("disks-root"));
mountMediaTab(document.getElementById("media-root"));
mountNetworksTab(document.getElementById("networks-root"));
mountVNFsTab(document.getElementById("vnfs-root"));

startInventory();
