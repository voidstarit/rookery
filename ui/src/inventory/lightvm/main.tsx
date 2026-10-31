import { render } from "preact";
import { LightVMTab } from "./LightVMTab";

export function mountLightVMTab(root: Element | null): void {
  if (root) {
    render(<LightVMTab />, root);
  }
}
