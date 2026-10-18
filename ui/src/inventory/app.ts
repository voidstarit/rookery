// @ts-nocheck -- transitional: this inventory UI is being migrated to Preact
// tab by tab. New code lives under ui/src and should be typed.
import { showToast as showAppToast } from "../shared/toast";

export function startInventory(): void {
  "use strict";

  const els = {
    vmCount: document.getElementById("vm-count"),
    apiStatus: document.getElementById("api-status"),
    apiStatusLabel: document.getElementById("api-status-label"),
    heroEyebrow: document.getElementById("hero-eyebrow"),
    heroTitle: document.getElementById("hero-title"),
    heroLede: document.getElementById("hero-lede"),
    inventorySummary: document.getElementById("inventory-summary"),
    errorBanner: document.getElementById("error-banner"),
    table: document.getElementById("vm-table"),
    vmRows: document.getElementById("vm-rows"),
    emptyState: document.getElementById("empty-state"),
    refresh: document.getElementById("refresh"),
    newVM: document.getElementById("new-vm"),
    newVMHint: document.getElementById("new-vm-hint"),
    emptyCreate: document.getElementById("empty-create"),
    readinessSummary: document.getElementById("readiness-summary"),
    thinPoolCard: document.getElementById("thin-pool-card"),
    thinPoolState: document.getElementById("thin-pool-state"),
    thinPoolTarget: document.getElementById("thin-pool-target"),
    thinPoolDetail: document.getElementById("thin-pool-detail"),
    vppCard: document.getElementById("vpp-card"),
    vppState: document.getElementById("vpp-state"),
    vppTarget: document.getElementById("vpp-target"),
    vppDetail: document.getElementById("vpp-detail"),
    tabVMs: document.getElementById("tab-vms"),
    tabDisks: document.getElementById("tab-disks"),
    tabMedia: document.getElementById("tab-media"),
    tabNetworks: document.getElementById("tab-networks"),
    tabVNFs: document.getElementById("tab-vnfs"),
    vmTabCount: document.getElementById("vm-tab-count"),
    vmsPanel: document.getElementById("vms-panel"),
    disksPanel: document.getElementById("disks-panel"),
    mediaPanel: document.getElementById("media-panel"),
    networksPanel: document.getElementById("networks-panel"),
    vnfsPanel: document.getElementById("vnfs-panel"),
    newDisk: document.getElementById("new-disk"),
    newMedia: document.getElementById("new-media"),
    newNetwork: document.getElementById("new-network"),
    dialog: document.getElementById("vm-dialog"),
    form: document.getElementById("vm-form"),
    dialogEyebrow: document.getElementById("dialog-eyebrow"),
    dialogTitle: document.getElementById("dialog-title"),
    formError: document.getElementById("form-error"),
    saveVM: document.getElementById("save-vm"),
    closeDialog: document.getElementById("close-dialog"),
    cancelDialog: document.getElementById("cancel-dialog"),
    id: document.getElementById("vm-id"),
    name: document.getElementById("vm-name"),
    description: document.getElementById("vm-description"),
    status: document.getElementById("vm-status"),
    vcpus: document.getElementById("vm-vcpus"),
    memory: document.getElementById("vm-memory"),
    disk: document.getElementById("vm-disk"),
    diskAdd: document.getElementById("vm-disk-add"),
    diskList: document.getElementById("vm-disk-list"),
    diskHint: document.getElementById("vm-disk-hint"),
    bootMedia: document.getElementById("vm-boot-media"),
    consoleEnabled: document.getElementById("vm-console-enabled"),
    vmMonitorDialog: document.getElementById("vm-monitor-dialog"),
    vmMonitorTitle: document.getElementById("vm-monitor-title"),
    vmMonitorLede: document.getElementById("vm-monitor-lede"),
    vmMonitorError: document.getElementById("vm-monitor-error"),
    vmMonitorStatus: document.getElementById("vm-monitor-status"),
    vmMonitorPID: document.getElementById("vm-monitor-pid"),
    vmMonitorCPU: document.getElementById("vm-monitor-cpu"),
    vmMonitorVCPUs: document.getElementById("vm-monitor-vcpus"),
    vmMonitorMemory: document.getElementById("vm-monitor-memory"),
    vmMonitorMemoryDetail: document.getElementById("vm-monitor-memory-detail"),
    vmMonitorRead: document.getElementById("vm-monitor-read"),
    vmMonitorReadTotal: document.getElementById("vm-monitor-read-total"),
    vmMonitorWrite: document.getElementById("vm-monitor-write"),
    vmMonitorWriteTotal: document.getElementById("vm-monitor-write-total"),
    vmMonitorNetwork: document.getElementById("vm-monitor-network"),
    vmMonitorNetworkDetail: document.getElementById("vm-monitor-network-detail"),
    vmMonitorUptime: document.getElementById("vm-monitor-uptime"),
    vmMonitorCPUChart: document.getElementById("vm-monitor-cpu-chart"),
    vmMonitorMemoryChart: document.getElementById("vm-monitor-memory-chart"),
    vmMonitorIOChart: document.getElementById("vm-monitor-io-chart"),
    vmMonitorNetworkChart: document.getElementById("vm-monitor-network-chart"),
    vmMonitorSampled: document.getElementById("vm-monitor-sampled"),
    refreshVMMonitor: document.getElementById("refresh-vm-monitor"),
    closeVMMonitor: document.getElementById("close-vm-monitor"),
    closeVMMonitorDialog: document.getElementById("close-vm-monitor-dialog"),
    vmInterfacesField: document.getElementById("vm-interfaces-field"),
    vmInterfaceSelect: document.getElementById("vm-interface-select"),
    vmInterfaceAdd: document.getElementById("vm-interface-add"),
    vmInterfaceList: document.getElementById("vm-interface-list"),
    vmInterfacesHint: document.getElementById("vm-interfaces-hint"),
  };

  const dateFormat = new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
  const numberFormat = new Intl.NumberFormat();

  const tabMeta = {
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

  let vms = [];
  let disks = [];
  let media = [];
  let interfaces = [];
  let vnfs = [];
  let editingID = null;
  let monitorVMID = null;
  let monitorVMTimer = null;
  let monitorVMLast = null;
  let monitorVMSamples = [];
  let loadingVMMetrics = false;
  let vmInterfaceSelection = new Map();
  let vmDiskSelection = new Map();
  let saving = false;
  let prerequisitesReady = false;
  let thinPoolReady = false;
  let vppReady = false;
  let activeTab = "vms";

  async function api(path, options = {}) {
    const headers = new Headers(options.headers || {});
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
    }

    const response = await fetch(path, { ...options, headers });
    let data = null;
    if (response.status !== 204) {
      const text = await response.text();
      if (text) {
        try {
          data = JSON.parse(text);
        } catch {
          data = null;
        }
      }
    }

    if (!response.ok) {
      const message =
        data && typeof data.error === "string"
          ? data.error
          : `Request failed (${response.status})`;
      const err = new Error(message);
      err.status = response.status;
      throw err;
    }

    return data;
  }

  function setAPIState(state) {
    const labels = {
      online: "api online",
      offline: "api offline",
      pending: "connecting",
    };
    els.apiStatus.dataset.state = state;
    els.apiStatusLabel.textContent = labels[state] || labels.pending;
  }

  function showBanner(message) {
    els.errorBanner.textContent = message;
    els.errorBanner.hidden = false;
  }

  function hideBanner() {
    els.errorBanner.hidden = true;
    els.errorBanner.textContent = "";
  }

  function showToast(message, isError = false) {
    showAppToast(message, isError);
  }

  function formatMemory(mb) {
    if (mb >= 1024) {
      const gb = mb / 1024;
      return `${Number.isInteger(gb) ? gb : gb.toFixed(1)} GB`;
    }
    return `${numberFormat.format(mb)} MB`;
  }

  function formatDate(value) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "—" : dateFormat.format(date);
  }

  function formatDuration(seconds) {
    if (!Number.isFinite(seconds) || seconds <= 0) {
      return "—";
    }
    const total = Math.floor(seconds);
    const days = Math.floor(total / 86400);
    const hours = Math.floor((total % 86400) / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const secs = total % 60;
    if (days > 0) return `${days}d ${hours}h`;
    if (hours > 0) return `${hours}h ${minutes}m`;
    if (minutes > 0) return `${minutes}m ${secs}s`;
    return `${secs}s`;
  }

  function formatBytes(bytes) {
    if (!Number.isFinite(bytes) || bytes < 0) {
      return "—";
    }
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit += 1;
    }
    const precision = unit === 0 || value >= 10 ? 0 : 1;
    return `${value.toFixed(precision)} ${units[unit]}`;
  }

  function sortVMs() {
    vms.sort((a, b) =>
      a.name.localeCompare(b.name, undefined, { sensitivity: "base" })
    );
  }

  function sortDisks() {
    disks.sort((a, b) =>
      a.name.localeCompare(b.name, undefined, { sensitivity: "base" })
    );
  }

  function sortMedia() {
    media.sort((a, b) =>
      a.name.localeCompare(b.name, undefined, { sensitivity: "base" })
    );
  }

  function sortInterfaces() {
    interfaces.sort((a, b) =>
      a.name.localeCompare(b.name, undefined, { sensitivity: "base" })
    );
  }

  function sortVNFs() {
    vnfs.sort((a, b) =>
      a.name.localeCompare(b.name, undefined, { sensitivity: "base" })
    );
  }

  function vmByID(vmID) {
    return vms.find((vm) => vm.id === vmID);
  }

  function diskOwner(diskID) {
    return vms.find((vm) => {
      if (vm.disk_id === diskID) return true;
      return Array.isArray(vm.disks) && vm.disks.some((disk) => disk.id === diskID);
    });
  }

  function availableDisks(currentVMID) {
    return disks.filter((disk) => {
      const owner = diskOwner(disk.id);
      return !owner || owner.id === currentVMID;
    });
  }

  function upsertVM(vm) {
    const index = vms.findIndex((item) => item.id === vm.id);
    if (index === -1) {
      vms.push(vm);
    } else {
      vms[index] = vm;
    }
    sortVMs();
    render();
  }

  function updateVMActionState() {
    const free = availableDisks(null).length;
    const blocked = !prerequisitesReady;
    const noDisks = free === 0;
    const enabled = !blocked && !noDisks;

    let hint = "";
    if (blocked) {
      hint = "New VM is blocked until the thin pool and FD.io VPP are ready.";
    } else if (noDisks) {
      hint = "No unattached disks. Create one on the Disks tab to enable New VM.";
    }

    els.newVM.disabled = !enabled;
    els.emptyCreate.disabled = !enabled;
    els.newVM.title = hint;
    els.emptyCreate.title = hint;
    els.newVMHint.textContent = hint;
    els.newVMHint.hidden = !hint || activeTab !== "vms";
  }

  function render() {
    renderVMs();
    updateVMActionState();
  }

  function renderVMs() {
    const running = vms.filter((vm) => vm.status === "running").length;
    els.vmCount.textContent = `${vms.length} VM${vms.length === 1 ? "" : "s"}`;
    els.vmTabCount.textContent = String(vms.length);
    els.inventorySummary.textContent =
      vms.length === 0
        ? "No instances registered"
        : `${vms.length} instance${vms.length === 1 ? "" : "s"} · ${running} running`;

    els.table.hidden = vms.length === 0;
    els.emptyState.hidden = vms.length !== 0;
    els.vmRows.replaceChildren();

    for (const vm of vms) {
      els.vmRows.append(renderVMRow(vm));
    }
  }

  function renderVMRow(vm) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("span");
    name.className = "vm-name";
    name.textContent = vm.name;
    nameCell.append(name);
    if (vm.description) {
      const description = document.createElement("span");
      description.className = "vm-description";
      description.textContent = vm.description;
      description.title = vm.description;
      nameCell.append(description);
    }

    const statusCell = document.createElement("td");
    const status = document.createElement("span");
    status.className = "status-badge";
    status.dataset.status = vm.status;
    status.textContent = vm.status;
    statusCell.append(status);

    const vcpuCell = document.createElement("td");
    vcpuCell.className = "mono";
    vcpuCell.textContent = numberFormat.format(vm.vcpus);

    const memoryCell = document.createElement("td");
    memoryCell.className = "mono";
    memoryCell.textContent = formatMemory(vm.memory_mb);

    const vmDisks = Array.isArray(vm.disks) && vm.disks.length
      ? vm.disks
      : vm.disk
        ? [vm.disk]
        : [];
    const diskCell = document.createElement("td");
    diskCell.className = vmDisks.length ? "mono" : "mono muted";
    diskCell.textContent = vmDisks.length
      ? vmDisks.map((disk) => disk.name).join(", ")
      : "—";
    if (vmDisks.length) {
      diskCell.title = vmDisks
        .map((disk) => `${disk.name} · ${numberFormat.format(disk.size_gb)} GB`)
        .join(", ");
    }

    const bootMedia = media.find((item) => item.id === vm.boot_media_id) || vm.boot_media;
    const bootCell = document.createElement("td");
    bootCell.className = bootMedia ? "mono" : "mono muted";
    bootCell.textContent = bootMedia ? bootMedia.name : "disk";
    if (bootMedia) {
      bootCell.title = bootMedia.path;
    }

    const consoleCell = document.createElement("td");
    consoleCell.className = vm.console_enabled ? "mono" : "mono muted";
    consoleCell.textContent = vm.console_enabled ? "enabled" : "disabled";

    const vmInterfaces = Array.isArray(vm.interfaces) ? vm.interfaces : [];
    const networkCell = document.createElement("td");
    networkCell.className = vmInterfaces.length ? "mono" : "mono muted";
    networkCell.textContent = vmInterfaces.length
      ? vmInterfaces.map((iface) => iface.network_name || iface.name).join(", ")
      : "none";
    if (vmInterfaces.length) {
      networkCell.title = vmInterfaces
        .map((iface) => `${iface.name} (${iface.network_name})`)
        .join(", ");
    }

    const updatedCell = document.createElement("td");
    updatedCell.className = "updated hide-sm";
    updatedCell.textContent = formatDate(vm.updated_at);

    const actionsCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "actions";

    const active = vm.status === "running" || vm.status === "starting";
    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = active ? "row-action danger" : "row-action";
    toggle.dataset.action = active ? "stop" : "start";
    toggle.dataset.id = vm.id;
    toggle.textContent = active ? "Stop" : "Start";
    toggle.setAttribute("aria-label", `${active ? "Stop" : "Start"} ${vm.name}`);

    const consoleButton = document.createElement("button");
    consoleButton.type = "button";
    consoleButton.className = "row-action";
    consoleButton.dataset.action = "console";
    consoleButton.dataset.id = vm.id;
    consoleButton.textContent = "Console";
    consoleButton.setAttribute("aria-label", `Open console for ${vm.name}`);
    if (!vm.console_enabled) {
      consoleButton.disabled = true;
      consoleButton.title = "Console access is disabled. Edit the VM to enable it.";
    } else if (vm.status !== "running") {
      consoleButton.disabled = true;
      consoleButton.title = "Start the VM to open its console.";
    }

    const monitor = document.createElement("button");
    monitor.type = "button";
    monitor.className = "row-action";
    monitor.dataset.action = "monitor";
    monitor.dataset.id = vm.id;
    monitor.textContent = "Monitor";
    monitor.setAttribute("aria-label", `Monitor ${vm.name}`);

    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "row-action";
    edit.dataset.action = "edit";
    edit.dataset.id = vm.id;
    edit.textContent = "Edit";
    edit.setAttribute("aria-label", `Edit ${vm.name}`);

    const del = document.createElement("button");
    del.type = "button";
    del.className = "row-action danger";
    del.dataset.action = "delete";
    del.dataset.id = vm.id;
    del.textContent = "Delete";
    del.setAttribute("aria-label", `Delete ${vm.name}`);

    actions.append(toggle, consoleButton, monitor, edit, del);
    actionsCell.append(actions);

    row.append(
      nameCell,
      statusCell,
      vcpuCell,
      memoryCell,
      diskCell,
      bootCell,
      consoleCell,
      networkCell,
      updatedCell,
      actionsCell
    );
    return row;
  }

  async function loadVMs() {
    els.refresh.disabled = true;
    const previousLabel = els.refresh.textContent;
    els.refresh.textContent = "Refreshing…";

    try {
      const data = await api("/api/vms");
      vms = Array.isArray(data) ? data : [];
      sortVMs();
      render();
      hideBanner();
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      showBanner(`Could not load VMs: ${err.message}`);
      throw err;
    } finally {
      els.refresh.disabled = false;
      els.refresh.textContent = previousLabel;
    }
  }

  async function loadDisks() {
    try {
      const data = await api("/api/disks");
      disks = Array.isArray(data) ? data : [];
      sortDisks();
      window.dispatchEvent(new CustomEvent("rookery:disks-data", { detail: disks }));
      if (els.dialog.open) {
        renderVMDiskPicker(currentDialogVM());
      }
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      throw err;
    }
  }

  async function loadMedia() {
    try {
      const data = await api("/api/media");
      media = Array.isArray(data) ? data : [];
      sortMedia();
      window.dispatchEvent(new CustomEvent("rookery:media-data", { detail: media }));
      if (els.dialog.open) {
        const current = currentDialogVM();
        populateBootMediaOptions(current?.boot_media_id ?? null, current?.boot_media ?? null);
      }
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      throw err;
    }
  }

  async function loadVNFs() {
    try {
      const data = await api("/api/vnfs");
      vnfs = Array.isArray(data) ? data : [];
      sortVNFs();
      setAPIState("online");
      window.dispatchEvent(new CustomEvent("rookery:vnfs-data", { detail: vnfs }));
    } catch (err) {
      setAPIState("offline");
      throw err;
    }
  }

  async function loadInterfaces() {
    try {
      const data = await api("/api/interfaces");
      interfaces = Array.isArray(data) ? data : [];
      sortInterfaces();
      window.dispatchEvent(new CustomEvent("rookery:interfaces-data", { detail: interfaces }));
      if (els.dialog.open) {
        renderVMInterfacePicker(currentDialogVM());
      }
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      throw err;
    }
  }

  const readinessLabels = {
    ready: "ready",
    checking: "checking",
    missing: "missing",
    not_configured: "not configured",
    unreachable: "unreachable",
    error: "error",
  };

  function setReadiness(ready, thinPool) {
    prerequisitesReady = ready;
    thinPoolReady = thinPool;
    updateVMActionState();
  }

  function setVPPReady(ready) {
    if (vppReady === ready) {
      return;
    }
    vppReady = ready;
    window.dispatchEvent(new CustomEvent("rookery:vpp-ready", { detail: ready }));
  }

  function applyReadinessCard(card, stateEl, targetEl, detailEl, component, target) {
    const state = (component && component.state) || "checking";
    card.dataset.state = state;
    stateEl.textContent = readinessLabels[state] || state;
    targetEl.textContent = target;
    targetEl.title = target;

    let detail = (component && component.detail) || "";
    if (!detail && state === "checking") {
      detail = "Checking…";
    }
    detailEl.textContent = detail;
  }

  function thinPoolCard(status) {
    const component = status && status.thin_pool;
    let target = "not configured";
    if (component && component.vg && component.name) {
      target = `${component.vg}/${component.name}`;
    } else if (component && component.vg) {
      target = component.vg;
    }
    applyReadinessCard(
      els.thinPoolCard,
      els.thinPoolState,
      els.thinPoolTarget,
      els.thinPoolDetail,
      component,
      target
    );
  }

  function vppCard(status) {
    const component = status && status.vpp;
    const target =
      component && component.socket ? component.socket : "not configured";
    applyReadinessCard(
      els.vppCard,
      els.vppState,
      els.vppTarget,
      els.vppDetail,
      component,
      target
    );
  }

  function renderReadiness(status) {
    const ready = Boolean(status && status.ready);
    const poolReady = Boolean(status && status.thin_pool && status.thin_pool.ready);
    const vppUp = Boolean(status && status.vpp && status.vpp.ready);
    setReadiness(ready, poolReady);
    setVPPReady(vppUp);
    thinPoolCard(status);
    vppCard(status);

    const summary = els.readinessSummary;
    if (ready) {
      summary.dataset.state = "ready";
      summary.textContent = "ready for VMs";
      return;
    }

    const states = [
      status && status.thin_pool && status.thin_pool.state,
      status && status.vpp && status.vpp.state,
    ].filter(Boolean);

    if (states.includes("unreachable") || states.includes("error")) {
      summary.dataset.state = "unreachable";
    } else if (
      states.includes("missing") ||
      states.includes("not_configured")
    ) {
      summary.dataset.state = "missing";
    } else {
      summary.dataset.state = "checking";
    }

    summary.textContent =
      summary.dataset.state === "checking"
        ? "checking"
        : "VM creation blocked";
  }

  function renderReadinessUnavailable() {
    setReadiness(false, false);
    setVPPReady(false);
    applyReadinessCard(
      els.thinPoolCard,
      els.thinPoolState,
      els.thinPoolTarget,
      els.thinPoolDetail,
      { state: "unreachable", detail: "Cannot reach the API." },
      "status unavailable"
    );
    applyReadinessCard(
      els.vppCard,
      els.vppState,
      els.vppTarget,
      els.vppDetail,
      { state: "unreachable", detail: "Cannot reach the API." },
      "status unavailable"
    );
    els.readinessSummary.dataset.state = "unreachable";
    els.readinessSummary.textContent = "status unavailable";
  }

  async function refreshStatus() {
    try {
      const status = await api("/api/status");
      renderReadiness(status);
      setAPIState("online");
    } catch {
      setAPIState("offline");
      renderReadinessUnavailable();
    }
  }

  const TAB_ORDER = ["vms", "disks", "media", "networks", "vnfs"];
  const TAB_ELEMENTS = {
    vms: els.tabVMs,
    disks: els.tabDisks,
    media: els.tabMedia,
    networks: els.tabNetworks,
    vnfs: els.tabVNFs,
  };
  const PANEL_ELEMENTS = {
    vms: els.vmsPanel,
    disks: els.disksPanel,
    media: els.mediaPanel,
    networks: els.networksPanel,
    vnfs: els.vnfsPanel,
  };
  const CREATE_BUTTONS = {
    vms: els.newVM,
    disks: els.newDisk,
    media: els.newMedia,
    networks: els.newNetwork,
  };

  function selectTab(name, focus = false, updateHash = true) {
    if (!TAB_ORDER.includes(name)) {
      name = "vms";
    }
    activeTab = name;

    for (const key of TAB_ORDER) {
      const selected = key === name;
      TAB_ELEMENTS[key].setAttribute("aria-selected", String(selected));
      TAB_ELEMENTS[key].tabIndex = selected ? 0 : -1;
      PANEL_ELEMENTS[key].hidden = !selected;
      if (CREATE_BUTTONS[key]) {
        CREATE_BUTTONS[key].hidden = !selected;
      }
    }

    if (updateHash) {
      window.history.replaceState(null, "", `#${name}`);
    }

    const meta = tabMeta[name];
    els.heroEyebrow.textContent = meta.eyebrow;
    els.heroTitle.textContent = meta.title;
    els.heroLede.textContent = meta.lede;
    updateVMActionState();
    window.dispatchEvent(new CustomEvent("rookery:tab-changed", { detail: name }));

    if (focus) {
      TAB_ELEMENTS[name].focus();
    }
  }

  function handleTabKey(event) {
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
  }

  function renderVMDiskPicker(currentVM = null) {
    const running =
      currentVM &&
      (currentVM.status === "running" || currentVM.status === "starting");

    for (const [id] of [...vmDiskSelection.entries()]) {
      const fresh = disks.find((disk) => disk.id === id);
      if (fresh) {
        vmDiskSelection.set(id, fresh);
      } else {
        vmDiskSelection.delete(id);
      }
    }

    const available = disks.filter((disk) => {
      if (vmDiskSelection.has(disk.id)) {
        return false;
      }
      const owner = diskOwner(disk.id);
      return !owner || (currentVM && owner.id === currentVM.id);
    });

    els.disk.replaceChildren();
    const placeholder = document.createElement("option");
    placeholder.value = "";
    placeholder.textContent =
      available.length > 0 ? "Select a disk…" : "No disks available";
    els.disk.append(placeholder);
    for (const disk of available) {
      const option = document.createElement("option");
      option.value = String(disk.id);
      option.textContent = `${disk.name} · ${numberFormat.format(disk.size_gb)} GB`;
      els.disk.append(option);
    }
    els.disk.disabled = Boolean(running) || available.length === 0;
    els.diskAdd.disabled = Boolean(running) || available.length === 0;

    els.diskList.replaceChildren();
    if (vmDiskSelection.size === 0) {
      const empty = document.createElement("li");
      empty.className = "empty";
      empty.textContent = "No disks selected";
      els.diskList.append(empty);
    } else {
      let position = 0;
      for (const [id, disk] of vmDiskSelection) {
        const item = document.createElement("li");

        const label = document.createElement("span");
        label.className = "interface-list-name";
        label.textContent = disk.name;
        const meta = document.createElement("span");
        meta.className = "interface-list-network";
        meta.textContent = `${numberFormat.format(disk.size_gb)} GB${position === 0 ? " · boot" : ""}`;
        label.append(meta);

        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "row-action danger";
        remove.dataset.removeDisk = String(id);
        remove.textContent = "Remove";
        remove.setAttribute("aria-label", `Remove disk ${disk.name}`);
        remove.disabled = Boolean(running);

        item.append(label, remove);
        els.diskList.append(item);
        position += 1;
      }
    }

    if (running) {
      els.diskHint.textContent = "Stop the VM before changing its disks.";
    } else if (available.length === 0 && vmDiskSelection.size === 0) {
      els.diskHint.textContent = "Create a disk on the Disks tab first.";
    } else {
      els.diskHint.textContent =
        "Choose a disk and click Add. The first disk is the boot disk. Remove disks with the buttons below.";
    }
  }

  function populateBootMediaOptions(selectedID, selectedMedia = null) {
    els.bootMedia.replaceChildren();

    const none = document.createElement("option");
    none.value = "";
    none.textContent = "Boot from disk";
    els.bootMedia.append(none);

    let selectedFound = false;
    for (const item of media) {
      const option = document.createElement("option");
      option.value = String(item.id);
      option.textContent = `${item.name} · ${formatBytes(item.size_bytes)}`;
      els.bootMedia.append(option);
      if (selectedID !== null && selectedID !== undefined && item.id === selectedID) {
        selectedFound = true;
      }
    }

    if (!selectedFound && selectedMedia && selectedID !== null && selectedID !== undefined) {
      const option = document.createElement("option");
      option.value = String(selectedMedia.id);
      option.textContent = `${selectedMedia.name} · ${formatBytes(selectedMedia.size_bytes)}`;
      els.bootMedia.append(option);
    }

    els.bootMedia.value =
      selectedID !== null && selectedID !== undefined ? String(selectedID) : "";
  }

  function currentDialogVM() {
    return editingID !== null ? vms.find((vm) => vm.id === editingID) || null : null;
  }

  function renderVMInterfacePicker(currentVM = null) {
    const running =
      currentVM &&
      (currentVM.status === "running" || currentVM.status === "starting");
    const attachedIDs = new Set(
      currentVM && Array.isArray(currentVM.interfaces)
        ? currentVM.interfaces.map((iface) => iface.id)
        : []
    );

    for (const [id, selected] of [...vmInterfaceSelection.entries()]) {
      const fresh = interfaces.find((iface) => iface.id === id);
      if (fresh) {
        vmInterfaceSelection.set(id, fresh);
      } else {
        vmInterfaceSelection.delete(id);
      }
    }

    const available = interfaces.filter((iface) => {
      if (vmInterfaceSelection.has(iface.id)) {
        return false;
      }
      return !iface.attached_vm_id || attachedIDs.has(iface.id);
    });

    els.vmInterfaceSelect.replaceChildren();
    const placeholder = document.createElement("option");
    placeholder.value = "";
    placeholder.textContent =
      available.length > 0 ? "Select an interface…" : "No interfaces available";
    els.vmInterfaceSelect.append(placeholder);
    for (const iface of available) {
      const option = document.createElement("option");
      option.value = String(iface.id);
      option.textContent = `${iface.name} · ${iface.network_name}`;
      els.vmInterfaceSelect.append(option);
    }
    els.vmInterfaceSelect.disabled = Boolean(running) || available.length === 0;
    els.vmInterfaceAdd.disabled = Boolean(running) || available.length === 0;

    els.vmInterfaceList.replaceChildren();
    if (vmInterfaceSelection.size === 0) {
      const empty = document.createElement("li");
      empty.className = "empty";
      empty.textContent = "No interfaces selected";
      els.vmInterfaceList.append(empty);
    } else {
      for (const [id, iface] of vmInterfaceSelection) {
        const item = document.createElement("li");

        const label = document.createElement("span");
        label.className = "interface-list-name";
        label.textContent = iface.name;
        const network = document.createElement("span");
        network.className = "interface-list-network";
        network.textContent = iface.network_name;
        label.append(network);

        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "row-action danger";
        remove.dataset.removeInterface = String(id);
        remove.textContent = "Remove";
        remove.setAttribute("aria-label", `Remove interface ${iface.name}`);
        remove.disabled = Boolean(running);

        item.append(label, remove);
        els.vmInterfaceList.append(item);
      }
    }

    if (running) {
      els.vmInterfacesHint.textContent = "Stop the VM before changing network interfaces.";
    } else if (available.length === 0 && vmInterfaceSelection.size === 0) {
      els.vmInterfacesHint.textContent =
        "Create a Rookery Virtual Interface on the Network tab first.";
    } else {
      els.vmInterfacesHint.textContent =
        "Choose an interface and click Add. Remove interfaces with the buttons below.";
    }
  }

  function openCreate() {
    if (!prerequisitesReady) {
      showToast("VM creation is blocked until prerequisites are ready.", true);
      return;
    }
    if (availableDisks(null).length === 0) {
      showToast("Create an unattached disk on the Disks tab first.", true);
      selectTab("disks");
      return;
    }

    editingID = null;
    els.form.reset();
    els.id.value = "";
    els.consoleEnabled.checked = false;
    vmDiskSelection.clear();
    renderVMDiskPicker(null);
    populateBootMediaOptions(null);
    els.vmInterfacesField.hidden = false;
    vmInterfaceSelection.clear();
    renderVMInterfacePicker(null);
    els.dialogEyebrow.textContent = "New instance";
    els.dialogTitle.textContent = "Create VM";
    els.saveVM.textContent = "Create VM";
    els.formError.hidden = true;
    els.formError.textContent = "";
    els.dialog.showModal();
    els.name.focus();
  }

  function openEdit(vm) {
    editingID = vm.id;
    els.form.reset();
    els.id.value = String(vm.id);
    els.name.value = vm.name;
    els.description.value = vm.description || "";
    els.status.value = vm.status;
    els.vcpus.value = String(vm.vcpus);
    els.memory.value = String(vm.memory_mb);
    const editorDisks = Array.isArray(vm.disks) && vm.disks.length
      ? vm.disks
      : vm.disk
        ? [vm.disk]
        : vm.disk_id
          ? disks.filter((disk) => disk.id === vm.disk_id)
          : [];
    vmDiskSelection = new Map(editorDisks.map((disk) => [disk.id, disk]));
    renderVMDiskPicker(vm);
    populateBootMediaOptions(vm.boot_media_id, vm.boot_media);
    els.consoleEnabled.checked = Boolean(vm.console_enabled);
    els.vmInterfacesField.hidden = false;
    vmInterfaceSelection = new Map(
      (Array.isArray(vm.interfaces) ? vm.interfaces : []).map((iface) => [
        iface.id,
        iface,
      ])
    );
    renderVMInterfacePicker(vm);
    els.dialogEyebrow.textContent = "Edit instance";
    els.dialogTitle.textContent = `Edit ${vm.name}`;
    els.saveVM.textContent = "Save changes";
    els.formError.hidden = true;
    els.formError.textContent = "";
    els.dialog.showModal();
    els.name.focus();
    els.name.select();
  }

  function setSaving(value, label) {
    saving = value;
    els.saveVM.disabled = value;
    els.cancelDialog.disabled = value;
    els.closeDialog.disabled = value;
    if (value) {
      els.saveVM.textContent = label;
    } else {
      els.saveVM.textContent = editingID === null ? "Create VM" : "Save changes";
    }
  }

  async function deleteVM(vm) {
    const confirmed = window.confirm(
      `Delete "${vm.name}"?\n\nThis removes the inventory record only. The disk remains available.`
    );
    if (!confirmed) {
      return;
    }

    try {
      await api(`/api/vms/${vm.id}`, { method: "DELETE" });
      vms = vms.filter((item) => item.id !== vm.id);
      render();
      loadInterfaces().catch(() => {});
      hideBanner();
      showToast(`Deleted ${vm.name}`);
    } catch (err) {
      showBanner(`Could not delete ${vm.name}: ${err.message}`);
    }
  }

  async function startVM(vm) {
    try {
      const updated = await api(`/api/vms/${vm.id}/start`, { method: "POST" });
      upsertVM(updated);
      setAPIState("online");
      hideBanner();
      showToast(`Started ${vm.name}`);
    } catch (err) {
      showBanner(`Could not start ${vm.name}: ${err.message}`);
      loadVMs().catch(() => {});
    }
  }

  async function stopVM(vm) {
    try {
      const updated = await api(`/api/vms/${vm.id}/stop`, { method: "POST" });
      upsertVM(updated);
      setAPIState("online");
      hideBanner();
      showToast(`Stopped ${vm.name}`);
    } catch (err) {
      showBanner(`Could not stop ${vm.name}: ${err.message}`);
      loadVMs().catch(() => {});
    }
  }

  function openConsole(vm) {
    if (!vm.console_enabled) {
      showToast("Console access is disabled. Edit the VM to enable it.", true);
      return;
    }
    if (vm.status !== "running") {
      showToast("Start the VM before opening its console.", true);
      return;
    }
    window.open(
      `/console.html?vm=${encodeURIComponent(vm.id)}`,
      "_blank",
      "noopener"
    );
  }

  function chartCanvas(canvas) {
    if (!canvas || typeof canvas.getContext !== "function") {
      return null;
    }
    const context = canvas.getContext("2d");
    if (!context) {
      return null;
    }
    const dpr = window.devicePixelRatio || 1;
    const width = Math.max(280, Math.round(canvas.getBoundingClientRect().width || 620));
    const height = 150;
    const pixelWidth = Math.round(width * dpr);
    const pixelHeight = Math.round(height * dpr);
    if (canvas.width !== pixelWidth || canvas.height !== pixelHeight) {
      canvas.width = pixelWidth;
      canvas.height = pixelHeight;
    }
    context.setTransform(dpr, 0, 0, dpr, 0, 0);
    return { context, width, height };
  }

  function drawMetricChart(canvas, series, options = {}) {
    const prepared = chartCanvas(canvas);
    if (!prepared) {
      return;
    }
    const { context, width, height } = prepared;
    context.clearRect(0, 0, width, height);

    const padding = { left: 52, right: 10, top: 12, bottom: 18 };
    const plotWidth = Math.max(1, width - padding.left - padding.right);
    const plotHeight = Math.max(1, height - padding.top - padding.bottom);
    const format = options.format || ((value) => String(Math.round(value)));
    const allValues = series.flatMap((item) => item.values || []).filter((value) => Number.isFinite(value));
    let maxValue = options.max || 0;
    if (!maxValue) {
      maxValue = allValues.length ? Math.max(...allValues) * 1.15 : 0;
    }
    if (maxValue <= 0) {
      maxValue = 1;
    }

    context.strokeStyle = "rgba(148, 163, 184, 0.14)";
    context.fillStyle = "#94a3b8";
    context.font = "10px SFMono-Regular, Consolas, monospace";
    context.lineWidth = 1;
    for (let line = 0; line <= 2; line += 1) {
      const y = padding.top + (plotHeight * line) / 2;
      context.beginPath();
      context.moveTo(padding.left, y);
      context.lineTo(padding.left + plotWidth, y);
      context.stroke();
      const label = format(maxValue * (1 - line / 2));
      context.fillText(label, 4, y + 3);
    }

    const marker = Number(options.marker);
    if (Number.isFinite(marker) && marker > 0 && marker <= maxValue) {
      const y = padding.top + plotHeight - (marker / maxValue) * plotHeight;
      context.save();
      context.setLineDash([4, 4]);
      context.strokeStyle = "rgba(226, 232, 240, 0.35)";
      context.beginPath();
      context.moveTo(padding.left, y);
      context.lineTo(padding.left + plotWidth, y);
      context.stroke();
      context.restore();
    }

    let drewPoint = false;
    for (const item of series) {
      const values = item.values || [];
      if (values.length === 0) {
        continue;
      }
      context.strokeStyle = item.color;
      context.fillStyle = item.color;
      context.lineWidth = 2;
      context.lineJoin = "round";
      context.lineCap = "round";
      context.beginPath();
      let open = false;
      for (let index = 0; index < values.length; index += 1) {
        const value = values[index];
        if (!Number.isFinite(value)) {
          open = false;
          continue;
        }
        const x = values.length === 1
          ? padding.left + plotWidth
          : padding.left + (plotWidth * index) / (values.length - 1);
        const y = padding.top + plotHeight - Math.min(1, Math.max(0, value / maxValue)) * plotHeight;
        if (!open) {
          context.moveTo(x, y);
          open = true;
        } else {
          context.lineTo(x, y);
        }
        drewPoint = true;
      }
      context.stroke();
      if (values.length === 1 && Number.isFinite(values[0])) {
        const x = padding.left + plotWidth;
        const y = padding.top + plotHeight - Math.min(1, Math.max(0, values[0] / maxValue)) * plotHeight;
        context.beginPath();
        context.arc(x, y, 2.5, 0, Math.PI * 2);
        context.fill();
      }
    }
    if (!drewPoint) {
      context.fillStyle = "#64748b";
      context.font = "11px sans-serif";
      context.fillText("No samples yet", padding.left, padding.top + plotHeight / 2);
    }
  }

  function renderVMMonitorCharts() {
    const cpuValues = monitorVMSamples.map((sample) => sample.cpu);
    const memoryValues = monitorVMSamples.map((sample) => sample.rss);
    const readValues = monitorVMSamples.map((sample) => sample.read);
    const writeValues = monitorVMSamples.map((sample) => sample.write);
    const netReadValues = monitorVMSamples.map((sample) => sample.netRead);
    const netWriteValues = monitorVMSamples.map((sample) => sample.netWrite);

    const finiteCPU = cpuValues.filter((value) => Number.isFinite(value));
    const finiteMemory = memoryValues.filter((value) => Number.isFinite(value));
    const finiteIO = [...readValues, ...writeValues].filter((value) => Number.isFinite(value));
    const finiteNet = [...netReadValues, ...netWriteValues].filter((value) => Number.isFinite(value));
    const monitoredVM = vms.find((vm) => vm.id === monitorVMID);
    const configuredBytes = Math.max(0, Number(monitoredVM?.memory_mb || 0) * 1024 * 1024);

    drawMetricChart(els.vmMonitorCPUChart, [{ values: cpuValues, color: "#60a5fa" }], {
      max: Math.max(100, finiteCPU.length ? Math.max(...finiteCPU) * 1.15 : 0),
      format: (value) => `${Math.round(value)}%`,
    });
    drawMetricChart(els.vmMonitorMemoryChart, [{ values: memoryValues, color: "#34d399" }], {
      max: Math.max(configuredBytes, finiteMemory.length ? Math.max(...finiteMemory) * 1.15 : 0),
      marker: configuredBytes,
      format: formatBytes,
    });
    drawMetricChart(els.vmMonitorIOChart, [
      { values: readValues, color: "#f59e0b" },
      { values: writeValues, color: "#f472b6" },
    ], {
      max: finiteIO.length ? Math.max(...finiteIO) * 1.15 : 0,
      format: (value) => `${formatBytes(value)}/s`,
    });
    drawMetricChart(els.vmMonitorNetworkChart, [
      { values: netReadValues, color: "#38bdf8" },
      { values: netWriteValues, color: "#a78bfa" },
    ], {
      max: finiteNet.length ? Math.max(...finiteNet) * 1.15 : 0,
      format: (value) => `${formatBytes(value)}/s`,
    });
  }

  function openVMMonitor(vm) {
    monitorVMID = vm.id;
    monitorVMLast = null;
    monitorVMSamples = [];
    if (monitorVMTimer !== null) {
      window.clearInterval(monitorVMTimer);
      monitorVMTimer = null;
    }
    els.vmMonitorTitle.textContent = `Monitor · ${vm.name}`;
    els.vmMonitorLede.textContent = `${vm.vcpus} vCPU · ${formatMemory(vm.memory_mb)} · ${vm.status}`;
    els.vmMonitorError.hidden = true;
    els.vmMonitorError.textContent = "";
    els.vmMonitorStatus.textContent = "—";
    els.vmMonitorPID.textContent = "PID —";
    els.vmMonitorCPU.textContent = "—";
    els.vmMonitorVCPUs.textContent = `${vm.vcpus} vCPU`;
    els.vmMonitorMemory.textContent = "—";
    els.vmMonitorMemoryDetail.textContent = `${formatMemory(vm.memory_mb)} configured`;
    els.vmMonitorRead.textContent = "—";
    els.vmMonitorReadTotal.textContent = "—";
    els.vmMonitorWrite.textContent = "—";
    els.vmMonitorWriteTotal.textContent = "—";
    els.vmMonitorNetwork.textContent = "—";
    els.vmMonitorNetworkDetail.textContent = "—";
    els.vmMonitorUptime.textContent = "—";
    els.vmMonitorSampled.textContent = "Waiting for the first sample…";
    els.vmMonitorDialog.showModal();
    renderVMMonitorCharts();
    loadVMMetrics().catch(() => {});
    monitorVMTimer = window.setInterval(() => {
      loadVMMetrics().catch(() => {});
    }, 2000);
  }

  function closeVMMonitor() {
    if (monitorVMTimer !== null) {
      window.clearInterval(monitorVMTimer);
      monitorVMTimer = null;
    }
    monitorVMID = null;
    monitorVMLast = null;
    monitorVMSamples = [];
    els.vmMonitorDialog.close();
  }

  async function loadVMMetrics() {
    if (monitorVMID === null || loadingVMMetrics) {
      return;
    }
    loadingVMMetrics = true;
    els.refreshVMMonitor.disabled = true;
    try {
      const data = await api(`/api/vms/${encodeURIComponent(monitorVMID)}/metrics`);
      const now = Date.now();
      const cpuTotal = Number(data.cpu_seconds_total) || 0;
      const readTotal = Number(data.disk_read_bytes_total) || 0;
      const writeTotal = Number(data.disk_write_bytes_total) || 0;
      const netReadTotal = Number(data.network_rx_bytes_total) || 0;
      const netWriteTotal = Number(data.network_tx_bytes_total) || 0;
      let cpuPercent = null;
      let readRate = null;
      let writeRate = null;
      let netReadRate = null;
      let netWriteRate = null;
      if (monitorVMLast && now > monitorVMLast.at) {
        const seconds = (now - monitorVMLast.at) / 1000;
        if (cpuTotal >= monitorVMLast.cpu) {
          cpuPercent = ((cpuTotal - monitorVMLast.cpu) / seconds) * 100;
        }
        if (readTotal >= monitorVMLast.read) {
          readRate = (readTotal - monitorVMLast.read) / seconds;
        }
        if (writeTotal >= monitorVMLast.write) {
          writeRate = (writeTotal - monitorVMLast.write) / seconds;
        }
        if (netReadTotal >= monitorVMLast.netRead) {
          netReadRate = (netReadTotal - monitorVMLast.netRead) / seconds;
        }
        if (netWriteTotal >= monitorVMLast.netWrite) {
          netWriteRate = (netWriteTotal - monitorVMLast.netWrite) / seconds;
        }
      }

      if (!data.running) {
        monitorVMLast = null;
        monitorVMSamples = [];
        els.vmMonitorStatus.textContent = "stopped";
        els.vmMonitorStatus.dataset.status = "stopped";
        els.vmMonitorPID.textContent = "PID —";
        els.vmMonitorCPU.textContent = "—";
        els.vmMonitorMemory.textContent = "—";
        els.vmMonitorMemoryDetail.textContent = `${formatMemory(data.memory_mb)} configured`;
        els.vmMonitorRead.textContent = "—";
        els.vmMonitorReadTotal.textContent = "—";
        els.vmMonitorWrite.textContent = "—";
        els.vmMonitorWriteTotal.textContent = "—";
        els.vmMonitorNetwork.textContent = "—";
        els.vmMonitorNetworkDetail.textContent = "—";
        els.vmMonitorUptime.textContent = "—";
        els.vmMonitorSampled.textContent = "VM is not running.";
        renderVMMonitorCharts();
      } else {
        monitorVMLast = { at: now, cpu: cpuTotal, read: readTotal, write: writeTotal, netRead: netReadTotal, netWrite: netWriteTotal };
        els.vmMonitorStatus.textContent = "running";
        els.vmMonitorStatus.dataset.status = "running";
        els.vmMonitorPID.textContent = `PID ${data.pid || "—"}`;
        els.vmMonitorCPU.textContent = cpuPercent === null ? "sampling…" : `${cpuPercent.toFixed(1)}%`;
        els.vmMonitorVCPUs.textContent = `${data.vcpus} vCPU`;
        els.vmMonitorMemory.textContent = formatBytes(Number(data.memory_rss_bytes) || 0);
        els.vmMonitorMemoryDetail.textContent =
          `${formatMemory(data.memory_mb)} configured · ${formatBytes(Number(data.memory_virtual_bytes) || 0)} virtual`;
        els.vmMonitorRead.textContent = readRate === null ? "sampling…" : `${formatBytes(readRate)}/s`;
        els.vmMonitorReadTotal.textContent = `${formatBytes(readTotal)} total`;
        els.vmMonitorWrite.textContent = writeRate === null ? "sampling…" : `${formatBytes(writeRate)}/s`;
        els.vmMonitorWriteTotal.textContent = `${formatBytes(writeTotal)} total`;
        els.vmMonitorNetwork.textContent = netReadRate === null
          ? "sampling…"
          : `↓ ${formatBytes(netReadRate)}/s · ↑ ${formatBytes(netWriteRate)}/s`;
        els.vmMonitorNetworkDetail.textContent =
          `RX ${formatBytes(netReadTotal)} · TX ${formatBytes(netWriteTotal)}`;
        els.vmMonitorUptime.textContent = formatDuration(Number(data.uptime_seconds) || 0);
        els.vmMonitorSampled.textContent = `Sampled ${new Date(data.sampled_at).toLocaleTimeString()}`;
        monitorVMSamples.push({
          cpu: cpuPercent,
          rss: Number(data.memory_rss_bytes) || 0,
          read: readRate,
          write: writeRate,
          netRead: netReadRate,
          netWrite: netWriteRate,
        });
        if (monitorVMSamples.length > 60) {
          monitorVMSamples.shift();
        }
        renderVMMonitorCharts();
      }
      els.vmMonitorError.hidden = true;
      els.vmMonitorError.textContent = "";
    } catch (err) {
      els.vmMonitorError.textContent = `Could not read VM metrics: ${err.message}`;
      els.vmMonitorError.hidden = false;
    } finally {
      loadingVMMetrics = false;
      els.refreshVMMonitor.disabled = false;
    }
  }

  els.form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (saving) {
      return;
    }

    if (editingID === null && !prerequisitesReady) {
      els.formError.textContent =
        "VM creation is blocked until the thin pool and FD.io VPP are ready.";
      els.formError.hidden = false;
      return;
    }

    els.formError.hidden = true;
    els.formError.textContent = "";

    if (!els.form.reportValidity()) {
      return;
    }

    const isEdit = editingID !== null;
    const payload = {
      name: els.name.value.trim(),
      description: els.description.value.trim(),
      status: els.status.value,
      vcpus: Number(els.vcpus.value),
      memory_mb: Number(els.memory.value),
      console_enabled: els.consoleEnabled.checked,
    };

    const selectedInterfaceIDs = Array.from(vmInterfaceSelection.keys());
    const selectedDiskIDs = Array.from(vmDiskSelection.keys());
    if (selectedDiskIDs.length === 0) {
      els.formError.textContent = "Select at least one disk.";
      els.formError.hidden = false;
      return;
    }

    if (!isEdit) {
      payload.disk_ids = selectedDiskIDs;
      if (selectedInterfaceIDs.length > 0) {
        payload.interface_ids = selectedInterfaceIDs;
      }
    } else {
      const currentVM = vms.find((item) => item.id === editingID);
      const running =
        currentVM &&
        (currentVM.status === "running" || currentVM.status === "starting");
      if (!running) {
        payload.disk_ids = selectedDiskIDs;
        payload.interface_ids = selectedInterfaceIDs;
      }
    }

    const bootMediaID = Number(els.bootMedia.value);
    if (bootMediaID) {
      payload.boot_media_id = bootMediaID;
    }

    setSaving(true, isEdit ? "Saving…" : "Creating…");

    try {
      const vm = await api(
        isEdit ? `/api/vms/${editingID}` : "/api/vms",
        {
          method: isEdit ? "PUT" : "POST",
          body: JSON.stringify(payload),
        }
      );
      upsertVM(vm);
      loadInterfaces().catch(() => {});
      setAPIState("online");
      hideBanner();
      els.dialog.close();
      showToast(isEdit ? `Saved ${vm.name}` : `Created ${vm.name}`);
    } catch (err) {
      els.formError.textContent = err.message;
      els.formError.hidden = false;
      if (err.status === 503) {
        refreshStatus();
      }
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSaving(false);
    }
  });


  els.vmRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }

    const vm = vms.find((item) => item.id === button.dataset.id);
    if (!vm) {
      return;
    }

    if (button.dataset.action === "edit") {
      openEdit(vm);
    } else if (button.dataset.action === "delete") {
      deleteVM(vm);
    } else if (button.dataset.action === "start") {
      startVM(vm);
    } else if (button.dataset.action === "stop") {
      stopVM(vm);
    } else if (button.dataset.action === "console") {
      openConsole(vm);
    } else if (button.dataset.action === "monitor") {
      openVMMonitor(vm);
    }
  });



  els.tabVMs.addEventListener("click", () => selectTab("vms"));
  els.tabDisks.addEventListener("click", () => selectTab("disks"));
  els.tabMedia.addEventListener("click", () => selectTab("media"));
  els.tabNetworks.addEventListener("click", () => selectTab("networks"));
  els.tabVNFs.addEventListener("click", () => selectTab("vnfs"));
  els.tabVMs.addEventListener("keydown", handleTabKey);
  els.tabDisks.addEventListener("keydown", handleTabKey);
  els.tabMedia.addEventListener("keydown", handleTabKey);
  els.tabNetworks.addEventListener("keydown", handleTabKey);
  els.tabVNFs.addEventListener("keydown", handleTabKey);
  els.newVM.addEventListener("click", openCreate);
  els.emptyCreate.addEventListener("click", openCreate);
  els.newDisk.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("rookery:add-disk"));
  });
  els.newMedia.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("rookery:add-media"));
  });
  els.newNetwork.addEventListener("click", () => {
    window.dispatchEvent(new CustomEvent("rookery:add-network"));
  });
  els.vmInterfaceAdd.addEventListener("click", () => {
    const id = Number(els.vmInterfaceSelect.value);
    if (!id) {
      return;
    }
    const iface = interfaces.find((item) => item.id === id);
    if (!iface) {
      return;
    }
    vmInterfaceSelection.set(id, iface);
    renderVMInterfacePicker(currentDialogVM());
  });
  els.vmInterfaceList.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-remove-interface]");
    if (!button || button.disabled) {
      return;
    }
    vmInterfaceSelection.delete(Number(button.dataset.removeInterface));
    renderVMInterfacePicker(currentDialogVM());
  });
  els.diskAdd.addEventListener("click", () => {
    const id = Number(els.disk.value);
    if (!id) {
      return;
    }
    const disk = disks.find((item) => item.id === id);
    if (!disk) {
      return;
    }
    vmDiskSelection.set(id, disk);
    renderVMDiskPicker(currentDialogVM());
  });
  els.diskList.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-remove-disk]");
    if (!button || button.disabled) {
      return;
    }
    vmDiskSelection.delete(Number(button.dataset.removeDisk));
    renderVMDiskPicker(currentDialogVM());
  });
  els.refresh.addEventListener("click", () => {
    loadVMs().catch(() => {});
    loadDisks().catch(() => {});
    loadMedia().catch(() => {});
    loadInterfaces().catch(() => {});
    loadVNFs().catch(() => {});
    refreshStatus();
  });

  els.closeDialog.addEventListener("click", () => {
    if (!saving) {
      els.dialog.close();
    }
  });
  els.cancelDialog.addEventListener("click", () => {
    if (!saving) {
      els.dialog.close();
    }
  });
  els.dialog.addEventListener("click", (event) => {
    if (event.target === els.dialog && !saving) {
      els.dialog.close();
    }
  });
  els.dialog.addEventListener("cancel", (event) => {
    if (saving) {
      event.preventDefault();
    }
  });




  els.closeVMMonitorDialog.addEventListener("click", () => {
    closeVMMonitor();
  });
  els.closeVMMonitor.addEventListener("click", () => {
    closeVMMonitor();
  });
  els.refreshVMMonitor.addEventListener("click", () => {
    monitorVMLast = null;
    monitorVMSamples = [];
    renderVMMonitorCharts();
    loadVMMetrics().catch(() => {});
  });
  els.vmMonitorDialog.addEventListener("click", (event) => {
    if (event.target === els.vmMonitorDialog) {
      closeVMMonitor();
    }
  });
  window.addEventListener("resize", () => {
    if (monitorVMID !== null) {
      renderVMMonitorCharts();
    }
  });
  els.vmMonitorDialog.addEventListener("close", () => {
    if (monitorVMTimer !== null) {
      window.clearInterval(monitorVMTimer);
      monitorVMTimer = null;
    }
    monitorVMID = null;
    monitorVMLast = null;
    monitorVMSamples = [];
  });



  window.addEventListener("rookery:disks-changed", (event) => {
    disks = Array.isArray(event.detail) ? event.detail : [];
    sortDisks();
    if (els.dialog.open) {
      renderVMDiskPicker(currentDialogVM());
    }
  });
  window.addEventListener("rookery:interfaces-changed", (event) => {
    interfaces = Array.isArray(event.detail) ? event.detail : [];
    sortInterfaces();
    if (els.dialog.open) {
      renderVMInterfacePicker(currentDialogVM());
    }
  });
  window.addEventListener("rookery:media-changed", (event) => {
    media = Array.isArray(event.detail) ? event.detail : [];
    sortMedia();
    if (els.dialog.open) {
      const current = currentDialogVM();
      populateBootMediaOptions(current?.boot_media_id ?? null, current?.boot_media ?? null);
    }
  });
  window.addEventListener("rookery:vnfs-changed", (event) => {
    vnfs = Array.isArray(event.detail) ? event.detail : [];
    sortVNFs();
  });
  window.addEventListener("rookery:open-tab", (event) => {
    const name = String(event.detail || "");
    if (TAB_ORDER.includes(name)) {
      selectTab(name);
    }
  });

  window.setInterval(() => {
    if (!document.hidden) {
      refreshStatus();
    }
  }, 10000);

  function tabFromHash() {
    const name = window.location.hash.replace(/^#/, "");
    return TAB_ORDER.includes(name) ? name : "vms";
  }

  selectTab(tabFromHash(), false, false);
  window.addEventListener("hashchange", () => {
    selectTab(tabFromHash(), false, false);
  });

  refreshStatus();
  Promise.allSettled([loadVMs(), loadDisks(), loadMedia(), loadInterfaces(), loadVNFs()]);
}
