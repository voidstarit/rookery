import type { StatusResponse } from "./types";
import { api } from "../shared/api";
import { currentRoute, navigate, ROUTE_EVENT } from "./router";

const TAB_ORDER = ["vms", "disks", "media", "networks", "vnfs"] as const;
type TabName = (typeof TAB_ORDER)[number];

type TabMeta = {
  eyebrow: string;
  title: string;
  lede: string;
};

const tabMeta: Record<TabName, TabMeta> = {
  vms: {
    eyebrow: "Thin-LVM inventory",
    title: "Virtual machines",
    lede: "Create instances, attach disks and installation media, start them with QEMU/KVM, and open their consoles.",
  },
  disks: {
    eyebrow: "Thin-LVM inventory",
    title: "Disks",
    lede: "Create and manage instance disks separately, then attach an unattached disk to a VM.",
  },
  media: {
    eyebrow: "Installation media",
    title: "Installation media",
    lede: "Register read-only ISO files from the host, then choose one as a VM's boot media.",
  },
  networks: {
    eyebrow: "VPP bridge domains",
    title: "Network",
    lede: "Create VPP bridge domains and vhost-user interfaces, then attach them to stopped VMs.",
  },
  vnfs: {
    eyebrow: "Network functions",
    title: "VNFs",
    lede: "Attach DHCPv4 and DHCPv6 functions to Rookery Networks, enable them, and inspect their leases.",
  },
};

function required<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`missing UI element #${id}`);
  }
  return element as T;
}

export function startInventory(): void {
  const apiStatus = required<HTMLElement>("api-status");
  const apiStatusLabel = required<HTMLElement>("api-status-label");
  const heroEyebrow = required<HTMLElement>("hero-eyebrow");
  const heroTitle = required<HTMLElement>("hero-title");
  const heroLede = required<HTMLElement>("hero-lede");
  const readinessSummary = required<HTMLElement>("readiness-summary");
  const thinPoolCard = required<HTMLElement>("thin-pool-card");
  const thinPoolState = required<HTMLElement>("thin-pool-state");
  const thinPoolTarget = required<HTMLElement>("thin-pool-target");
  const thinPoolDetail = required<HTMLElement>("thin-pool-detail");
  const vppCard = required<HTMLElement>("vpp-card");
  const vppState = required<HTMLElement>("vpp-state");
  const vppTarget = required<HTMLElement>("vpp-target");
  const vppDetail = required<HTMLElement>("vpp-detail");

  const tabElements: Record<TabName, HTMLButtonElement> = {
    vms: required<HTMLButtonElement>("tab-vms"),
    disks: required<HTMLButtonElement>("tab-disks"),
    media: required<HTMLButtonElement>("tab-media"),
    networks: required<HTMLButtonElement>("tab-networks"),
    vnfs: required<HTMLButtonElement>("tab-vnfs"),
  };
  const panelElements: Record<TabName, HTMLElement> = {
    vms: required("vms-panel"),
    disks: required("disks-panel"),
    media: required("media-panel"),
    networks: required("networks-panel"),
    vnfs: required("vnfs-panel"),
  };
  const createButtons: Partial<Record<TabName, HTMLButtonElement>> = {
    vms: required<HTMLButtonElement>("new-vm"),
    disks: required<HTMLButtonElement>("new-disk"),
    media: required<HTMLButtonElement>("new-media"),
    networks: required<HTMLButtonElement>("new-network"),
  };

  let activeTab: TabName = "vms";

  const readinessLabels: Record<string, string> = {
    ready: "ready",
    checking: "checking",
    missing: "missing",
    not_configured: "not configured",
    unreachable: "unreachable",
    error: "error",
  };

  const setAPIState = (state: "online" | "offline" | "pending") => {
    const labels = {
      online: "api online",
      offline: "api offline",
      pending: "connecting",
    };
    apiStatus.dataset.state = state;
    apiStatusLabel.textContent = labels[state];
  };

  const applyReadinessCard = (
    card: HTMLElement,
    stateEl: HTMLElement,
    targetEl: HTMLElement,
    detailEl: HTMLElement,
    component: { state?: string; detail?: string } | undefined,
    target: string
  ) => {
    const state = component?.state || "checking";
    card.dataset.state = state;
    stateEl.textContent = readinessLabels[state] || state;
    targetEl.textContent = target;
    targetEl.title = target;
    let detail = component?.detail || "";
    if (!detail && state === "checking") {
      detail = "Checking…";
    }
    detailEl.textContent = detail;
  };

  const renderReadiness = (status: StatusResponse) => {
    const ready = Boolean(status.ready);
    const thinPool = status.thin_pool;
    const vpp = status.vpp;

    let poolTarget = "not configured";
    if (thinPool?.vg && thinPool.name) {
      poolTarget = `${thinPool.vg}/${thinPool.name}`;
    } else if (thinPool?.vg) {
      poolTarget = thinPool.vg;
    }
    applyReadinessCard(thinPoolCard, thinPoolState, thinPoolTarget, thinPoolDetail, thinPool, poolTarget);
    applyReadinessCard(
      vppCard,
      vppState,
      vppTarget,
      vppDetail,
      vpp,
      vpp?.socket || "not configured"
    );

    if (ready) {
      readinessSummary.dataset.state = "ready";
      readinessSummary.textContent = "ready for VMs";
    } else {
      const states = [thinPool?.state, vpp?.state].filter(Boolean) as string[];
      if (states.includes("unreachable") || states.includes("error")) {
        readinessSummary.dataset.state = "unreachable";
      } else if (states.includes("missing") || states.includes("not_configured")) {
        readinessSummary.dataset.state = "missing";
      } else {
        readinessSummary.dataset.state = "checking";
      }
      readinessSummary.textContent =
        readinessSummary.dataset.state === "checking" ? "checking" : "VM creation blocked";
    }

    const thinPoolReady = Boolean(thinPool?.ready);
    const vppReady = Boolean(vpp?.ready);
    window.dispatchEvent(
      new CustomEvent("rookery:readiness-changed", {
        detail: { ready, thinPool: thinPoolReady },
      })
    );
    window.dispatchEvent(new CustomEvent("rookery:vpp-ready", { detail: vppReady }));
  };

  const renderReadinessUnavailable = () => {
    applyReadinessCard(
      thinPoolCard,
      thinPoolState,
      thinPoolTarget,
      thinPoolDetail,
      { state: "unreachable", detail: "Cannot reach the API." },
      "status unavailable"
    );
    applyReadinessCard(
      vppCard,
      vppState,
      vppTarget,
      vppDetail,
      { state: "unreachable", detail: "Cannot reach the API." },
      "status unavailable"
    );
    readinessSummary.dataset.state = "unreachable";
    readinessSummary.textContent = "status unavailable";
    window.dispatchEvent(
      new CustomEvent("rookery:readiness-changed", { detail: { ready: false, thinPool: false } })
    );
    window.dispatchEvent(new CustomEvent("rookery:vpp-ready", { detail: false }));
  };

  const refreshStatus = async () => {
    try {
      const status = await api<StatusResponse>("/api/status");
      renderReadiness(status);
      setAPIState("online");
    } catch {
      setAPIState("offline");
      renderReadinessUnavailable();
    }
  };

  const isTab = (value: string): value is TabName =>
    (TAB_ORDER as readonly string[]).includes(value);

  const tabForPath = (path: string): TabName => {
    const top = path.split("/").filter(Boolean)[0] || "vms";
    if (top === "storage-backends") {
      return "disks";
    }
    if (top === "interfaces" || top === "acls" || top === "route-tables") {
      return "networks";
    }
    return isTab(top) ? top : "vms";
  };

  const selectTab = (name: string, focus = false, updateURL = true) => {
    const selectedTab = isTab(name) ? name : "vms";
    const changed = selectedTab !== activeTab;
    activeTab = selectedTab;

    for (const key of TAB_ORDER) {
      const selected = key === selectedTab;
      tabElements[key].setAttribute("aria-selected", String(selected));
      tabElements[key].tabIndex = selected ? 0 : -1;
      panelElements[key].hidden = !selected;
      const button = createButtons[key];
      if (button) {
        button.hidden = !selected;
      }
    }

    if (updateURL) {
      navigate(`/${selectedTab}`);
    }

    const meta = tabMeta[selectedTab];
    heroEyebrow.textContent = meta.eyebrow;
    heroTitle.textContent = meta.title;
    heroLede.textContent = meta.lede;
    document.title = `${meta.title} · rookery`;
    if (changed) {
      window.dispatchEvent(new CustomEvent("rookery:tab-changed", { detail: selectedTab }));
    }

    if (focus) {
      tabElements[selectedTab].focus();
    }
  };

  const handleTabKey = (event: KeyboardEvent) => {
    const handled = ["ArrowLeft", "ArrowRight", "Home", "End"];
    if (!handled.includes(event.key)) {
      return;
    }
    event.preventDefault();
    const current = TAB_ORDER.indexOf(activeTab);
    if (event.key === "Home") {
      selectTab(TAB_ORDER[0], true);
    } else if (event.key === "End") {
      selectTab(TAB_ORDER[TAB_ORDER.length - 1], true);
    } else if (event.key === "ArrowLeft") {
      selectTab(TAB_ORDER[(current - 1 + TAB_ORDER.length) % TAB_ORDER.length], true);
    } else {
      selectTab(TAB_ORDER[(current + 1) % TAB_ORDER.length], true);
    }
  };

  const syncRoute = () => {
    const route = currentRoute();
    if (route.path === "/") {
      window.history.replaceState(null, "", "/vms");
    }
    // Route-table detail/create pages are full-page views. Hiding the
    // inventory chrome makes the URL a true refreshable page instead of a
    // panel embedded inside the Networks tab.
    document.body.classList.toggle("route-page-mode", route.path.startsWith("/route-tables"));
    selectTab(tabForPath(route.path), false, false);
  };

  for (const key of TAB_ORDER) {
    tabElements[key].addEventListener("click", () => selectTab(key));
    tabElements[key].addEventListener("keydown", handleTabKey);
  }

  createButtons.vms?.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("rookery:add-vm"));
  });
  createButtons.disks?.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("rookery:add-disk"));
  });
  createButtons.media?.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("rookery:add-media"));
  });
  createButtons.networks?.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("rookery:add-network"));
  });

  window.addEventListener("popstate", syncRoute);
  window.addEventListener(ROUTE_EVENT, syncRoute);
  window.addEventListener("rookery:open-tab", (event) => {
    const name = String((event as CustomEvent<string>).detail || "");
    if (isTab(name)) {
      selectTab(name);
    }
  });

  window.setInterval(() => {
    if (!document.hidden) {
      void refreshStatus();
    }
  }, 10000);

  // Keep old #vms-style bookmarks working while moving to path routes.
  const legacyHash = window.location.hash.replace(/^#/, "");
  if (legacyHash && isTab(legacyHash)) {
    window.history.replaceState(null, "", `/${legacyHash}`);
  }
  syncRoute();
  void refreshStatus();
}
