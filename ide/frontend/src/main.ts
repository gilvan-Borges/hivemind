import "@fontsource-variable/material-symbols-outlined";
import "./styles.css";

import { boot } from "./boot";
import { isOrbRuntime, mountOrbWindow } from "./orb/view";

if (isOrbRuntime()) {
  mountOrbWindow();
} else {
  boot();
}
