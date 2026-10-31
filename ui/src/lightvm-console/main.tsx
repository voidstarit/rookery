import { render } from "preact";
import { LightVMConsoleApp } from "./LightVMConsoleApp";

const root = document.getElementById("lightvm-console-root");
if (root) {
  render(<LightVMConsoleApp />, root);
}
