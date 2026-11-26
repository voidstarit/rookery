import { render } from "preact";
import { SettingsTab } from "./SettingsTab";

/**
 * mountSettingsTab mounts the SettingsTab island into the supplied container and is a no-op when
 * the container is null.
 */
export function mountSettingsTab(root: HTMLElement | null): void {
  if (root) {
    render(<SettingsTab />, root);
  }
}
