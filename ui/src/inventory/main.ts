import { mountToastHost } from "../shared/toast";
import { startInventory } from "./app";
import { mountVNFsTab } from "./vnfs/main";

const toastRoot = document.getElementById("toast-root");
if (toastRoot) {
  mountToastHost(toastRoot);
}

mountVNFsTab(document.getElementById("vnfs-root"));

startInventory();
