import { render } from "preact";
import { SettingsTab } from "./SettingsTab";

export function mountSettingsTab(root: HTMLElement | null): void {
  if (root) {
    render(<SettingsTab />, root);
  }
}
