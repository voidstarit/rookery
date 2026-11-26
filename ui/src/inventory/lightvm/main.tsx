import { render } from "preact";
import { LightVMTab } from "./LightVMTab";

/**
 * mountLightVMTab renders the LightVM tab into the given root element, doing nothing when it is
 * null.
 */
export function mountLightVMTab(root: Element | null): void {
  if (root) {
    render(<LightVMTab />, root);
  }
}
