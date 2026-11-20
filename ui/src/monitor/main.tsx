import { render } from "preact";
import {
  WorkloadMonitorPage,
  type WorkloadKind,
} from "../inventory/WorkloadMonitorDialog";

const root = document.getElementById("monitor-root");
const params = new URLSearchParams(window.location.search);
const kind: WorkloadKind = params.get("kind") === "lightvm" ? "LightVM" : "VM";
const id = params.get("id") || "";

if (root) {
  render(<WorkloadMonitorPage kind={kind} id={id} />, root);
}
