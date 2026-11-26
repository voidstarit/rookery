import { render } from "preact";
import { MediaTab } from "./MediaTab";

/**
 * mountMediaTab mounts the MediaTab island into the supplied container and is a no-op when the
 * container is null.
 */
export function mountMediaTab(root: Element | null): void {
  if (root) {
    render(<MediaTab />, root);
  }
}
