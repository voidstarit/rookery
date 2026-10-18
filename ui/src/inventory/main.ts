import { mountToastHost } from "../shared/toast";
import { startInventory } from "./app";
import { mountMediaTab } from "./media/main";
import { mountVNFsTab } from "./vnfs/main";

const toastRoot = document.getElementById("toast-root");
if (toastRoot) {
  mountToastHost(toastRoot);
}

mountMediaTab(document.getElementById("media-root"));
mountVNFsTab(document.getElementById("vnfs-root"));

startInventory();
