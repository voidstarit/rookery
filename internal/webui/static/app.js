(() => {
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
    diskTabCount: document.getElementById("disk-tab-count"),
    mediaTabCount: document.getElementById("media-tab-count"),
    networkTabCount: document.getElementById("network-tab-count"),
    vnfTabCount: document.getElementById("vnf-tab-count"),
    vmsPanel: document.getElementById("vms-panel"),
    disksPanel: document.getElementById("disks-panel"),
    mediaPanel: document.getElementById("media-panel"),
    networksPanel: document.getElementById("networks-panel"),
    vnfsPanel: document.getElementById("vnfs-panel"),
    newDisk: document.getElementById("new-disk"),
    newStorageBackend: document.getElementById("new-storage-backend"),
    emptyCreateStorageBackend: document.getElementById("empty-create-storage-backend"),
    refreshStorageBackends: document.getElementById("refresh-storage-backends"),
    storageBackendSummary: document.getElementById("storage-backend-summary"),
    storageBackendErrorBanner: document.getElementById("storage-backend-error-banner"),
    storageBackendTable: document.getElementById("storage-backend-table"),
    storageBackendRows: document.getElementById("storage-backend-rows"),
    storageBackendEmptyState: document.getElementById("storage-backend-empty-state"),
    storageBackendDialog: document.getElementById("storage-backend-dialog"),
    storageBackendForm: document.getElementById("storage-backend-form"),
    storageBackendDialogEyebrow: document.getElementById("storage-backend-dialog-eyebrow"),
    storageBackendDialogTitle: document.getElementById("storage-backend-dialog-title"),
    storageBackendID: document.getElementById("storage-backend-id"),
    storageBackendName: document.getElementById("storage-backend-name"),
    storageBackendTitle: document.getElementById("storage-backend-title"),
    storageBackendDescription: document.getElementById("storage-backend-description"),
    storageBackendDriver: document.getElementById("storage-backend-driver"),
    storageBackendVG: document.getElementById("storage-backend-vg"),
    storageBackendPool: document.getElementById("storage-backend-pool"),
    storageBackendFormError: document.getElementById("storage-backend-form-error"),
    saveStorageBackend: document.getElementById("save-storage-backend"),
    closeStorageBackendDialog: document.getElementById("close-storage-backend-dialog"),
    cancelStorageBackendDialog: document.getElementById("cancel-storage-backend-dialog"),
    importDiskDialog: document.getElementById("import-disk-dialog"),
    importDiskLede: document.getElementById("import-disk-lede"),
    importDiskErrorBanner: document.getElementById("import-disk-error-banner"),
    importDiskList: document.getElementById("import-disk-list"),
    importDiskEmpty: document.getElementById("import-disk-empty"),
    importDisks: document.getElementById("import-disks"),
    refreshImportDisks: document.getElementById("refresh-import-disks"),
    closeImportDiskDialog: document.getElementById("close-import-disk-dialog"),
    cancelImportDiskDialog: document.getElementById("cancel-import-disk-dialog"),
    newMedia: document.getElementById("new-media"),
    newNetwork: document.getElementById("new-network"),
    newVNF: document.getElementById("new-vnf"),
    refreshDisks: document.getElementById("refresh-disks"),
    refreshMedia: document.getElementById("refresh-media"),
    refreshNetworks: document.getElementById("refresh-networks"),
    refreshVNFs: document.getElementById("refresh-vnfs"),
    diskSummary: document.getElementById("disk-summary"),
    mediaSummary: document.getElementById("media-summary"),
    networkSummary: document.getElementById("network-summary"),
    interfaceSummary: document.getElementById("interface-summary"),
    vnfSummary: document.getElementById("vnf-summary"),
    diskErrorBanner: document.getElementById("disk-error-banner"),
    mediaErrorBanner: document.getElementById("media-error-banner"),
    networkErrorBanner: document.getElementById("network-error-banner"),
    interfaceErrorBanner: document.getElementById("interface-error-banner"),
    vnfErrorBanner: document.getElementById("vnf-error-banner"),
    diskTable: document.getElementById("disk-table"),
    mediaTable: document.getElementById("media-table"),
    networkTable: document.getElementById("network-table"),
    interfaceTable: document.getElementById("interface-table"),
    vnfTable: document.getElementById("vnf-table"),
    diskRows: document.getElementById("disk-rows"),
    mediaRows: document.getElementById("media-rows"),
    networkRows: document.getElementById("network-rows"),
    interfaceRows: document.getElementById("interface-rows"),
    vnfRows: document.getElementById("vnf-rows"),
    diskEmptyState: document.getElementById("disk-empty-state"),
    mediaEmptyState: document.getElementById("media-empty-state"),
    networkEmptyState: document.getElementById("network-empty-state"),
    interfaceEmptyState: document.getElementById("interface-empty-state"),
    vnfEmptyState: document.getElementById("vnf-empty-state"),
    emptyCreateDisk: document.getElementById("empty-create-disk"),
    emptyCreateMedia: document.getElementById("empty-create-media"),
    emptyCreateNetwork: document.getElementById("empty-create-network"),
    emptyCreateVNF: document.getElementById("empty-create-vnf"),
    newInterface: document.getElementById("new-interface"),
    newACL: document.getElementById("new-acl"),
    emptyCreateACL: document.getElementById("empty-create-acl"),
    refreshACLs: document.getElementById("refresh-acls"),
    aclTable: document.getElementById("acl-table"),
    aclRows: document.getElementById("acl-rows"),
    aclEmptyState: document.getElementById("acl-empty-state"),
    aclSummary: document.getElementById("acl-summary"),
    aclErrorBanner: document.getElementById("acl-error-banner"),
    aclDialog: document.getElementById("acl-dialog"),
    aclForm: document.getElementById("acl-form"),
    aclDialogEyebrow: document.getElementById("acl-dialog-eyebrow"),
    aclDialogTitle: document.getElementById("acl-dialog-title"),
    aclID: document.getElementById("acl-id"),
    aclName: document.getElementById("acl-name"),
    aclTitle: document.getElementById("acl-title"),
    aclDescription: document.getElementById("acl-description"),
    aclRuleRows: document.getElementById("acl-rule-rows"),
    addACLRule: document.getElementById("add-acl-rule"),
    aclPreview: document.getElementById("acl-preview"),
    aclFormError: document.getElementById("acl-form-error"),
    saveACL: document.getElementById("save-acl"),
    closeACLDialog: document.getElementById("close-acl-dialog"),
    cancelACLDialog: document.getElementById("cancel-acl-dialog"),
    interfaceACLDialog: document.getElementById("interface-acl-dialog"),
    interfaceACLForm: document.getElementById("interface-acl-form"),
    interfaceACLTitle: document.getElementById("interface-acl-title"),
    interfaceACLLede: document.getElementById("interface-acl-lede"),
    interfaceIngressACL: document.getElementById("interface-ingress-acl"),
    interfaceEgressACL: document.getElementById("interface-egress-acl"),
    interfaceACLPreview: document.getElementById("interface-acl-preview"),
    interfaceACLFormError: document.getElementById("interface-acl-form-error"),
    saveInterfaceACL: document.getElementById("save-interface-acl"),
    closeInterfaceACLDialog: document.getElementById("close-interface-acl-dialog"),
    cancelInterfaceACL: document.getElementById("cancel-interface-acl"),
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
    diskBackend: document.getElementById("disk-backend"),
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
    diskDialog: document.getElementById("disk-dialog"),
    diskForm: document.getElementById("disk-form"),
    diskDialogEyebrow: document.getElementById("disk-dialog-eyebrow"),
    diskDialogTitle: document.getElementById("disk-dialog-title"),
    diskFormError: document.getElementById("disk-form-error"),
    saveDisk: document.getElementById("save-disk"),
    closeDiskDialog: document.getElementById("close-disk-dialog"),
    cancelDiskDialog: document.getElementById("cancel-disk-dialog"),
    diskName: document.getElementById("disk-name"),
    diskSize: document.getElementById("disk-size"),
    mediaDialog: document.getElementById("media-dialog"),
    mediaForm: document.getElementById("media-form"),
    mediaDialogEyebrow: document.getElementById("media-dialog-eyebrow"),
    mediaDialogTitle: document.getElementById("media-dialog-title"),
    mediaFormError: document.getElementById("media-form-error"),
    saveMedia: document.getElementById("save-media"),
    closeMediaDialog: document.getElementById("close-media-dialog"),
    cancelMediaDialog: document.getElementById("cancel-media-dialog"),
    mediaName: document.getElementById("media-name"),
    mediaPath: document.getElementById("media-path"),
    networkDialog: document.getElementById("network-dialog"),
    networkForm: document.getElementById("network-form"),
    networkName: document.getElementById("network-name"),
    networkFormError: document.getElementById("network-form-error"),
    saveNetwork: document.getElementById("save-network"),
    closeNetworkDialog: document.getElementById("close-network-dialog"),
    cancelNetworkDialog: document.getElementById("cancel-network-dialog"),
    vnfDialog: document.getElementById("vnf-dialog"),
    vnfDialogTitle: document.getElementById("vnf-dialog-title"),
    vnfForm: document.getElementById("vnf-form"),
    vnfID: document.getElementById("vnf-id"),
    vnfName: document.getElementById("vnf-name"),
    vnfNetwork: document.getElementById("vnf-network"),
    vnfType: document.getElementById("vnf-type"),
    vnfTypeHint: document.getElementById("vnf-type-hint"),
    vnfEnabled: document.getElementById("vnf-enabled"),
    vnfEnabledHint: document.getElementById("vnf-enabled-hint"),
    vnfGateway: document.getElementById("vnf-gateway"),
    vnfDHCPFields: document.getElementById("vnf-dhcp-fields"),
    vnfNAT44Fields: document.getElementById("vnf-nat44-fields"),
    vnfOutsideInterface: document.getElementById("vnf-outside-interface"),
    vnfOutsideHint: document.getElementById("vnf-outside-hint"),
    vnfOutsideAddress: document.getElementById("vnf-outside-address"),
    vnfOutsideGateway: document.getElementById("vnf-outside-gateway"),
    vnfNAT44Pool: document.getElementById("vnf-nat44-pool"),
    vnfDNS: document.getElementById("vnf-dns"),
    vnfRangeStart: document.getElementById("vnf-range-start"),
    vnfRangeEnd: document.getElementById("vnf-range-end"),
    vnfLeaseTime: document.getElementById("vnf-lease-time"),
    vnfFormError: document.getElementById("vnf-form-error"),
    saveVNF: document.getElementById("save-vnf"),
    closeVNFDialog: document.getElementById("close-vnf-dialog"),
    cancelVNFDialog: document.getElementById("cancel-vnf-dialog"),
    vnfLeasesDialog: document.getElementById("vnf-leases-dialog"),
    vnfLeasesTitle: document.getElementById("vnf-leases-title"),
    vnfLeasesLede: document.getElementById("vnf-leases-lede"),
    vnfLeasesError: document.getElementById("vnf-leases-error"),
    vnfLeaseTable: document.getElementById("vnf-lease-table"),
    vnfLeaseRows: document.getElementById("vnf-lease-rows"),
    vnfLeaseEmpty: document.getElementById("vnf-lease-empty"),
    refreshVNFLeases: document.getElementById("refresh-vnf-leases"),
    closeVNFLeases: document.getElementById("close-vnf-leases"),
    closeVNFLeasesDialog: document.getElementById("close-vnf-leases-dialog"),
    vnfSessionsDialog: document.getElementById("vnf-sessions-dialog"),
    vnfSessionsTitle: document.getElementById("vnf-sessions-title"),
    vnfSessionsLede: document.getElementById("vnf-sessions-lede"),
    vnfSessionsError: document.getElementById("vnf-sessions-error"),
    vnfSessionsText: document.getElementById("vnf-sessions-text"),
    refreshVNFSessions: document.getElementById("refresh-vnf-sessions"),
    closeVNFSessions: document.getElementById("close-vnf-sessions"),
    closeVNFSessionsDialog: document.getElementById("close-vnf-sessions-dialog"),
    interfaceDialog: document.getElementById("interface-dialog"),
    interfaceForm: document.getElementById("interface-form"),
    interfaceName: document.getElementById("interface-name"),
    interfaceNetwork: document.getElementById("interface-network"),
    interfaceVM: document.getElementById("interface-vm"),
    interfaceFormError: document.getElementById("interface-form-error"),
    saveInterface: document.getElementById("save-interface"),
    closeInterfaceDialog: document.getElementById("close-interface-dialog"),
    cancelInterfaceDialog: document.getElementById("cancel-interface-dialog"),
    attachDialog: document.getElementById("attach-dialog"),
    attachForm: document.getElementById("attach-form"),
    attachInterfaceName: document.getElementById("attach-interface-name"),
    attachVM: document.getElementById("attach-vm"),
    attachFormError: document.getElementById("attach-form-error"),
    saveAttach: document.getElementById("save-attach"),
    closeAttachDialog: document.getElementById("close-attach-dialog"),
    cancelAttachDialog: document.getElementById("cancel-attach-dialog"),
    toast: document.getElementById("toast"),
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
      lede: "Attach DHCPv4 and DHCPv6 functions to Nerd's Networks, enable them, and inspect their leases.",
    },
  };

  let vms = [];
  let disks = [];
  let storageBackends = [];
  let media = [];
  let networks = [];
  let interfaces = [];
  let vnfs = [];
  let hostInterfaces = [];
  let acls = [];
  let editingID = null;
  let editingInterfaceID = null;
  let editingACLID = null;
  let editingStorageBackendID = null;
  let importingBackendID = null;
  let importDiskItems = [];
  let importLoadID = 0;
  let savingImport = false;
  let assigningInterfaceACLID = null;
  let editingVNFID = null;
  let leasesVNFID = null;
  let sessionsVNFID = null;
  let monitorVMID = null;
  let monitorVMTimer = null;
  let monitorVMLast = null;
  let monitorVMSamples = [];
  let loadingVMMetrics = false;
  let vmInterfaceSelection = new Map();
  let vmDiskSelection = new Map();
  let saving = false;
  let savingDisk = false;
  let savingMedia = false;
  let savingNetwork = false;
  let savingInterface = false;
  let savingACL = false;
  let savingStorageBackend = false;
  let savingInterfaceACL = false;
  let savingAttach = false;
  let savingVNF = false;
  let loadingVNFLeases = false;
  let toastTimer = null;
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

  function showDiskBanner(message) {
    els.diskErrorBanner.textContent = message;
    els.diskErrorBanner.hidden = false;
  }

  function hideDiskBanner() {
    els.diskErrorBanner.hidden = true;
    els.diskErrorBanner.textContent = "";
  }

  function showMediaBanner(message) {
    els.mediaErrorBanner.textContent = message;
    els.mediaErrorBanner.hidden = false;
  }

  function hideMediaBanner() {
    els.mediaErrorBanner.hidden = true;
    els.mediaErrorBanner.textContent = "";
  }

  function showNetworkBanner(message) {
    els.networkErrorBanner.textContent = message;
    els.networkErrorBanner.hidden = false;
  }

  function hideNetworkBanner() {
    els.networkErrorBanner.hidden = true;
    els.networkErrorBanner.textContent = "";
  }

  function showACLBanner(message) {
    els.aclErrorBanner.textContent = message;
    els.aclErrorBanner.hidden = false;
  }

  function hideACLBanner() {
    els.aclErrorBanner.hidden = true;
    els.aclErrorBanner.textContent = "";
  }

  function showStorageBackendBanner(message) {
    els.storageBackendErrorBanner.textContent = message;
    els.storageBackendErrorBanner.hidden = false;
  }

  function hideStorageBackendBanner() {
    els.storageBackendErrorBanner.hidden = true;
    els.storageBackendErrorBanner.textContent = "";
  }

  function showInterfaceBanner(message) {
    els.interfaceErrorBanner.textContent = message;
    els.interfaceErrorBanner.hidden = false;
  }

  function hideInterfaceBanner() {
    els.interfaceErrorBanner.hidden = true;
    els.interfaceErrorBanner.textContent = "";
  }

  function showVNFBanner(message) {
    els.vnfErrorBanner.textContent = message;
    els.vnfErrorBanner.hidden = false;
  }

  function hideVNFBanner() {
    els.vnfErrorBanner.hidden = true;
    els.vnfErrorBanner.textContent = "";
  }

  function showToast(message, isError = false) {
    window.clearTimeout(toastTimer);
    els.toast.textContent = message;
    els.toast.classList.toggle("error", isError);
    els.toast.hidden = false;
    toastTimer = window.setTimeout(() => {
      els.toast.hidden = true;
    }, 3500);
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

  function mediaUsers(mediaID) {
    return vms.filter((vm) => vm.boot_media_id === mediaID);
  }

  function sortNetworks() {
    networks.sort((a, b) =>
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

  function networkByID(networkID) {
    return networks.find((network) => network.id === networkID);
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

  function upsertDisk(disk) {
    const index = disks.findIndex((item) => item.id === disk.id);
    if (index === -1) {
      disks.push(disk);
    } else {
      disks[index] = disk;
    }
    sortDisks();
    render();
  }

  function upsertMedia(item) {
    const index = media.findIndex((entry) => entry.id === item.id);
    if (index === -1) {
      media.push(item);
    } else {
      media[index] = item;
    }
    sortMedia();
    render();
  }

  function upsertNetwork(network) {
    const index = networks.findIndex((item) => item.id === network.id);
    if (index === -1) {
      networks.push(network);
    } else {
      networks[index] = network;
    }
    sortNetworks();
    render();
  }

  function upsertInterface(iface) {
    const index = interfaces.findIndex((item) => item.id === iface.id);
    if (index === -1) {
      interfaces.push(iface);
    } else {
      interfaces[index] = iface;
    }
    sortInterfaces();
    render();
  }

  function upsertVNF(vnf) {
    const index = vnfs.findIndex((item) => item.id === vnf.id);
    if (index === -1) {
      vnfs.push(vnf);
    } else {
      vnfs[index] = vnf;
    }
    sortVNFs();
    render();
  }

  function vnfByID(vnfID) {
    return vnfs.find((vnf) => vnf.id === vnfID);
  }

  function vnfsForNetwork(networkID) {
    return vnfs.filter((vnf) => vnf.network_id === networkID);
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

  function updateDiskActionState() {
    const hint = storageBackends.length === 0
      ? "Add a storage backend before creating disks"
      : "";
    els.newDisk.disabled = storageBackends.length === 0;
    els.emptyCreateDisk.disabled = storageBackends.length === 0;
    els.newDisk.title = hint;
    els.emptyCreateDisk.title = hint;
  }

  function updateNetworkActionState() {
    const hint = vppReady ? "" : "Network changes are blocked until FD.io VPP is ready";
    els.newNetwork.disabled = !vppReady;
    els.emptyCreateNetwork.disabled = !vppReady;
    els.newNetwork.title = hint;
    els.emptyCreateNetwork.title = hint;

    const ifaceHint =
      networks.length === 0
        ? "Create a Nerd's Network first"
        : !vppReady
          ? "Interface creation is blocked until FD.io VPP is ready"
          : "";
    els.newInterface.disabled = networks.length === 0 || !vppReady;
    els.newInterface.title = ifaceHint;

    const vnfHint =
      networks.length === 0
        ? "Create a Nerd's Network first"
        : !vppReady
          ? "VNFs can be created disabled, but enabling needs FD.io VPP"
          : "";
    els.newVNF.disabled = networks.length === 0;
    els.emptyCreateVNF.disabled = networks.length === 0;
    els.newVNF.title = vnfHint;
    els.emptyCreateVNF.title = vnfHint;

    const aclHint = vppReady ? "" : "ACL changes are blocked until FD.io VPP is ready";
    els.newACL.disabled = !vppReady;
    els.emptyCreateACL.disabled = !vppReady;
    els.newACL.title = aclHint;
    els.emptyCreateACL.title = aclHint;
  }

  function render() {
    renderVMs();
    renderDisks();
    renderStorageBackends();
    renderMedia();
    renderNetworks();
    renderACLs();
    renderVNFs();
    updateVMActionState();
    updateDiskActionState();
    updateNetworkActionState();
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

  function renderDisks() {
    const attached = disks.filter((disk) => diskOwner(disk.id)).length;
    els.diskTabCount.textContent = String(disks.length);
    els.diskSummary.textContent =
      disks.length === 0
        ? "No disks registered"
        : `${disks.length} disk${disks.length === 1 ? "" : "s"} · ${attached} attached`;

    els.diskTable.hidden = disks.length === 0;
    els.diskEmptyState.hidden = disks.length !== 0;
    els.diskRows.replaceChildren();

    for (const disk of disks) {
      els.diskRows.append(renderDiskRow(disk));
    }
  }

  function renderDiskRow(disk) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("span");
    name.className = "vm-name";
    name.textContent = disk.name;
    nameCell.append(name);
    if (disk.device) {
      const device = document.createElement("span");
      device.className = "vm-description";
      device.textContent = disk.device;
      device.title = disk.device;
      nameCell.append(device);
    }

    const sizeCell = document.createElement("td");
    sizeCell.className = "mono";
    sizeCell.textContent = `${numberFormat.format(disk.size_gb)} GB`;

    const backendCell = document.createElement("td");
    backendCell.className = disk.storage_backend_name ? "mono" : "mono muted";
    backendCell.textContent = disk.storage_backend_name || "—";

    const owner = diskOwner(disk.id);
    const attachedCell = document.createElement("td");
    attachedCell.className = owner ? "mono" : "mono muted";
    attachedCell.textContent = owner ? owner.name : "unattached";

    const updatedCell = document.createElement("td");
    updatedCell.className = "updated hide-sm";
    updatedCell.textContent = formatDate(disk.updated_at);

    const actionsCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "actions";

    const del = document.createElement("button");
    del.type = "button";
    del.className = "row-action danger";
    del.dataset.action = "delete-disk";
    del.dataset.id = String(disk.id);
    del.textContent = "Delete";
    del.setAttribute("aria-label", `Delete ${disk.name}`);
    if (owner) {
      del.disabled = true;
      del.title = `Detach ${disk.name} from ${owner.name} before deleting it.`;
    }

    actions.append(del);
    actionsCell.append(actions);

    row.append(nameCell, sizeCell, attachedCell, updatedCell, actionsCell);
    return row;
  }

  function renderStorageBackends() {
    const totalDisks = storageBackends.reduce((sum, backend) => sum + (backend.disk_count || 0), 0);
    els.storageBackendSummary.textContent =
      storageBackends.length === 0
        ? "No storage backends registered"
        : `${storageBackends.length} backend${storageBackends.length === 1 ? "" : "s"} · ${totalDisks} disk${totalDisks === 1 ? "" : "s"}`;
    els.storageBackendTable.hidden = storageBackends.length === 0;
    els.storageBackendEmptyState.hidden = storageBackends.length !== 0;
    els.storageBackendRows.replaceChildren();
    for (const backend of storageBackends) {
      els.storageBackendRows.append(renderStorageBackendRow(backend));
    }
    if (els.diskBackend) {
      populateDiskBackendOptions(els.diskBackend.value);
    }
  }

  function renderStorageBackendRow(backend) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("span");
    name.className = "vm-name";
    name.textContent = backend.name;
    nameCell.append(name);
    if (backend.title || backend.description) {
      const description = document.createElement("span");
      description.className = "vm-description";
      description.textContent = [backend.title, backend.description].filter(Boolean).join(" · ");
      description.title = backend.description || backend.title || "";
      nameCell.append(description);
    }

    const driverCell = document.createElement("td");
    driverCell.className = "mono";
    driverCell.textContent = backend.driver === "lvm-thin" ? "LVM thin" : backend.driver;

    const locationCell = document.createElement("td");
    locationCell.className = "mono";
    locationCell.textContent = `${backend.config?.vg || "—"}/${backend.config?.thin_pool || "—"}`;

    const diskCell = document.createElement("td");
    diskCell.className = backend.disk_count ? "mono" : "mono muted";
    diskCell.textContent = String(backend.disk_count || 0);

    const updatedCell = document.createElement("td");
    updatedCell.className = "updated hide-sm";
    updatedCell.textContent = formatDate(backend.updated_at);

    const actionsCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "actions";

    const importButton = document.createElement("button");
    importButton.type = "button";
    importButton.className = "row-action";
    importButton.dataset.action = "import-storage-backend";
    importButton.dataset.id = String(backend.id);
    importButton.textContent = "Import";
    if (backend.driver !== "lvm-thin" || !backend.config?.vg || !backend.config?.thin_pool) {
      importButton.disabled = true;
      importButton.title = "Import is available for configured LVM thin backends.";
    } else {
      importButton.title = "Import existing thin volumes from this backend.";
    }

    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "row-action";
    edit.dataset.action = "edit-storage-backend";
    edit.dataset.id = String(backend.id);
    edit.textContent = "Edit";

    const del = document.createElement("button");
    del.type = "button";
    del.className = "row-action danger";
    del.dataset.action = "delete-storage-backend";
    del.dataset.id = String(backend.id);
    del.textContent = "Delete";
    if (backend.disk_count > 0) {
      del.disabled = true;
      del.title = "Delete disks using this backend first.";
    }

    actions.append(importButton, edit, del);
    actionsCell.append(actions);
    row.append(nameCell, driverCell, locationCell, diskCell, updatedCell, actionsCell);
    return row;
  }

  function renderMedia() {
    const used = media.filter((item) => mediaUsers(item.id).length > 0).length;
    els.mediaTabCount.textContent = String(media.length);
    els.mediaSummary.textContent =
      media.length === 0
        ? "No installation media registered"
        : `${media.length} media file${media.length === 1 ? "" : "s"} · ${used} in use`;

    els.mediaTable.hidden = media.length === 0;
    els.mediaEmptyState.hidden = media.length !== 0;
    els.mediaRows.replaceChildren();

    for (const item of media) {
      els.mediaRows.append(renderMediaRow(item));
    }
  }

  function renderMediaRow(item) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("span");
    name.className = "vm-name";
    name.textContent = item.name;
    nameCell.append(name);
    if (item.path) {
      const path = document.createElement("span");
      path.className = "vm-description";
      path.textContent = item.path;
      path.title = item.path;
      nameCell.append(path);
    }

    const sizeCell = document.createElement("td");
    sizeCell.className = "mono";
    sizeCell.textContent = formatBytes(item.size_bytes);

    const users = mediaUsers(item.id);
    const usedCell = document.createElement("td");
    usedCell.className = users.length ? "mono" : "mono muted";
    if (users.length === 0) {
      usedCell.textContent = "not used";
    } else if (users.length === 1) {
      usedCell.textContent = users[0].name;
    } else {
      usedCell.textContent = `${users[0].name} +${users.length - 1}`;
    }
    if (users.length > 0) {
      usedCell.title = users.map((vm) => vm.name).join(", ");
    }

    const updatedCell = document.createElement("td");
    updatedCell.className = "updated hide-sm";
    updatedCell.textContent = formatDate(item.updated_at);

    const actionsCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "actions";

    const del = document.createElement("button");
    del.type = "button";
    del.className = "row-action danger";
    del.dataset.action = "delete-media";
    del.dataset.id = String(item.id);
    del.textContent = "Remove";
    del.setAttribute("aria-label", `Remove ${item.name}`);
    if (users.length > 0) {
      del.disabled = true;
      del.title = `Clear boot media on ${users.map((vm) => vm.name).join(", ")} first.`;
    }

    actions.append(del);
    actionsCell.append(actions);

    row.append(nameCell, sizeCell, usedCell, updatedCell, actionsCell);
    return row;
  }

  function renderNetworks() {
    const attached = interfaces.filter((iface) => iface.attached_vm_id).length;
    els.networkTabCount.textContent = String(networks.length);
    els.networkSummary.textContent =
      networks.length === 0
        ? "No Nerd's Networks registered"
        : `${networks.length} network${networks.length === 1 ? "" : "s"} · ${interfaces.length} interface${interfaces.length === 1 ? "" : "s"} · ${vnfs.length} VNF${vnfs.length === 1 ? "" : "s"}`;
    els.interfaceSummary.textContent =
      interfaces.length === 0
        ? "No virtual interfaces registered"
        : `${interfaces.length} interface${interfaces.length === 1 ? "" : "s"} · ${attached} attached`;

    els.networkTable.hidden = networks.length === 0;
    els.networkEmptyState.hidden = networks.length !== 0;
    els.networkRows.replaceChildren();
    for (const network of networks) {
      els.networkRows.append(renderNetworkRow(network));
    }

    els.interfaceTable.hidden = interfaces.length === 0;
    els.interfaceEmptyState.hidden = interfaces.length !== 0;
    els.interfaceRows.replaceChildren();
    for (const iface of interfaces) {
      els.interfaceRows.append(renderInterfaceRow(iface));
    }
  }

  function renderNetworkRow(network) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("span");
    name.className = "vm-name";
    name.textContent = network.name;
    nameCell.append(name);
    if (network.bvi_host_name) {
      const bvi = document.createElement("span");
      bvi.className = "vm-description";
      bvi.textContent = `${network.bvi_host_name}${network.ipv4_gateway ? ` · ${network.ipv4_gateway}` : ""}`;
      nameCell.append(bvi);
    }

    const bdCell = document.createElement("td");
    bdCell.className = "mono";
    bdCell.textContent = `bd ${network.bridge_domain_id}`;

    const count = interfaces.filter((iface) => iface.network_id === network.id).length;
    const countCell = document.createElement("td");
    countCell.className = count ? "mono" : "mono muted";
    countCell.textContent = String(count);

    const networkVNFs = vnfsForNetwork(network.id);
    const vnfCell = document.createElement("td");
    vnfCell.className = networkVNFs.length ? "mono" : "mono muted";
    vnfCell.textContent = networkVNFs.length
      ? networkVNFs.map((vnf) => `${vnf.name} (${vnf.type})`).join(", ")
      : "none";

    const updatedCell = document.createElement("td");
    updatedCell.className = "updated hide-sm";
    updatedCell.textContent = formatDate(network.updated_at);

    const actionsCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "actions";

    const addVNF = document.createElement("button");
    addVNF.type = "button";
    addVNF.className = "row-action";
    addVNF.dataset.action = "add-vnf";
    addVNF.dataset.id = String(network.id);
    addVNF.textContent = "Add VNF";
    addVNF.setAttribute("aria-label", `Add a VNF to ${network.name}`);

    const del = document.createElement("button");
    del.type = "button";
    del.className = "row-action danger";
    del.dataset.action = "delete-network";
    del.dataset.id = String(network.id);
    del.textContent = "Delete";
    del.setAttribute("aria-label", `Delete network ${network.name}`);
    if (count > 0) {
      del.disabled = true;
      del.title = "Delete the virtual interfaces first.";
    } else if (!vppReady) {
      del.disabled = true;
      del.title = "VPP must be ready to delete a bridge domain.";
    }

    actions.append(addVNF, del);
    actionsCell.append(actions);
    row.append(nameCell, bdCell, countCell, vnfCell, updatedCell, actionsCell);
    return row;
  }

  function sortACLs() {
    acls.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
  }

  function aclByName(name) {
    return acls.find((acl) => acl.name.toLowerCase() === String(name).toLowerCase());
  }

  function aclAssignments(acl) {
    return interfaces.filter(
      (iface) => iface.ingress_acl_name === acl.name || iface.egress_acl_name === acl.name
    );
  }

  function renderACLs() {
    const assigned = acls.filter((acl) => aclAssignments(acl).length > 0).length;
    els.aclSummary.textContent =
      acls.length === 0
        ? "No ACLs defined"
        : `${acls.length} ACL${acls.length === 1 ? "" : "s"} · ${assigned} assigned`;
    els.aclTable.hidden = acls.length === 0;
    els.aclEmptyState.hidden = acls.length !== 0;
    els.aclRows.replaceChildren();
    for (const acl of acls) {
      els.aclRows.append(renderACLRow(acl));
    }
  }

  function renderACLRow(acl) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("span");
    name.className = "vm-name";
    name.textContent = acl.name;
    nameCell.append(name);
    if (acl.description) {
      const description = document.createElement("span");
      description.className = "vm-description";
      description.textContent = acl.description;
      description.title = acl.description;
      nameCell.append(description);
    }

    const titleCell = document.createElement("td");
    titleCell.textContent = acl.title || "—";

    const ruleCell = document.createElement("td");
    ruleCell.className = "mono";
    ruleCell.textContent = `${acl.rules?.length || 0} rule${acl.rules?.length === 1 ? "" : "s"}`;

    const assignCell = document.createElement("td");
    const assignments = aclAssignments(acl);
    assignCell.className = assignments.length ? "mono" : "mono muted";
    assignCell.textContent = assignments.length
      ? assignments.map((iface) => iface.name).join(", ")
      : "none";

    const updatedCell = document.createElement("td");
    updatedCell.className = "updated hide-sm";
    updatedCell.textContent = formatDate(acl.updated_at);

    const actionsCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "actions";

    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "row-action";
    edit.dataset.action = "edit-acl";
    edit.dataset.id = String(acl.id);
    edit.textContent = "Edit / Preview";

    const del = document.createElement("button");
    del.type = "button";
    del.className = "row-action danger";
    del.dataset.action = "delete-acl";
    del.dataset.id = String(acl.id);
    del.textContent = "Delete";
    if (assignments.length > 0) {
      del.disabled = true;
      del.title = "Remove this ACL from its interfaces first.";
    }

    actions.append(edit, del);
    actionsCell.append(actions);
    row.append(nameCell, titleCell, ruleCell, assignCell, updatedCell, actionsCell);
    return row;
  }

  function formatACLRuleText(rule) {
    const action = String(rule.action || "").toUpperCase();
    const protocol = String(rule.protocol || "any").toLowerCase();
    let text = `${action} ${protocol} ${rule.source || "0.0.0.0/0"} -> ${rule.destination || "0.0.0.0/0"}`;
    if (protocol === "tcp" || protocol === "udp") {
      const sourceStart = Number(rule.source_port_start) || 0;
      const sourceEnd = Number(rule.source_port_end) || 0;
      const destinationStart = Number(rule.destination_port_start) || 0;
      const destinationEnd = Number(rule.destination_port_end) || 0;
      if (sourceStart || sourceEnd) {
        text += ` sports ${sourceStart || "*"}-${sourceEnd || sourceStart || "*"}`;
      }
      if (destinationStart || destinationEnd) {
        text += ` dports ${destinationStart || "*"}-${destinationEnd || destinationStart || "*"}`;
      }
    }
    if (rule.description) {
      text += `  — ${rule.description}`;
    }
    return text;
  }

  function formatACLText(acl, label = "ACL") {
    const lines = [
      `${label}: ${acl.name || "(unnamed)"}`,
      `Title: ${acl.title || "—"}`,
      `Description: ${acl.description || "—"}`,
      "Rules:",
    ];
    const rules = Array.isArray(acl.rules) ? acl.rules : [];
    if (rules.length === 0) {
      lines.push("  (no rules)");
    } else {
      rules.forEach((rule, index) => {
        lines.push(`  ${index + 1}. ${formatACLRuleText(rule)}`);
      });
    }
    lines.push("Default action: DENY (implicit)");
    return lines.join("\n");
  }

  function aclRuleFromForm(ruleElement) {
    const protocol = ruleElement.querySelector(".acl-rule-protocol").value;
    const portsEnabled = protocol === "tcp" || protocol === "udp";
    return {
      action: ruleElement.querySelector(".acl-rule-action").value,
      description: ruleElement.querySelector(".acl-rule-description").value.trim(),
      protocol,
      source: ruleElement.querySelector(".acl-rule-source").value.trim(),
      destination: ruleElement.querySelector(".acl-rule-destination").value.trim(),
      source_port_start: portsEnabled
        ? Number(ruleElement.querySelector(".acl-rule-source-start").value) || 0
        : 0,
      source_port_end: portsEnabled
        ? Number(ruleElement.querySelector(".acl-rule-source-end").value) || 0
        : 0,
      destination_port_start: portsEnabled
        ? Number(ruleElement.querySelector(".acl-rule-destination-start").value) || 0
        : 0,
      destination_port_end: portsEnabled
        ? Number(ruleElement.querySelector(".acl-rule-destination-end").value) || 0
        : 0,
    };
  }

  function readACLRules() {
    return Array.from(els.aclRuleRows.querySelectorAll(".acl-rule")).map(aclRuleFromForm);
  }

  function renumberACLRules() {
    Array.from(els.aclRuleRows.querySelectorAll(".acl-rule")).forEach((rule, index) => {
      const label = rule.querySelector(".acl-rule-label");
      if (label) {
        label.textContent = `Rule ${index + 1}`;
      }
    });
  }

  function addACLRule(rule = {}) {
    const wrapper = document.createElement("div");
    wrapper.className = "acl-rule";

    const head = document.createElement("div");
    head.className = "acl-rule-head";
    const label = document.createElement("span");
    label.className = "acl-rule-label";
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "row-action danger";
    remove.dataset.removeRule = "true";
    remove.textContent = "Remove";
    head.append(label, remove);

    const fields = document.createElement("div");
    fields.className = "acl-rule-fields";

    const appendSelect = (labelText, className, options, value) => {
      const icon = document.createElement("label");
      icon.className = `field acl-field-${className}`;
      const text = document.createElement("span");
      text.className = "field-label";
      text.textContent = labelText;
      const select = document.createElement("select");
      select.className = className;
      for (const [optionValue, optionLabel] of options) {
        const option = document.createElement("option");
        option.value = optionValue;
        option.textContent = optionLabel;
        select.append(option);
      }
      select.value = value;
      icon.append(text, select);
      return icon;
    };

    const appendInput = (labelText, className, value, placeholder) => {
      const icon = document.createElement("label");
      icon.className = `field acl-field-${className}`;
      const text = document.createElement("span");
      text.className = "field-label";
      text.textContent = labelText;
      const input = document.createElement("input");
      input.type = "text";
      input.className = className;
      input.value = value || "";
      input.placeholder = placeholder || "";
      input.autocomplete = "off";
      input.spellcheck = false;
      icon.append(text, input);
      return icon;
    };

    const appendPorts = (labelText, startClass, endClass, start, end) => {
      const icon = document.createElement("label");
      icon.className = `field acl-field-${startClass}`;
      const text = document.createElement("span");
      text.className = "field-label";
      text.textContent = labelText;
      const pair = document.createElement("div");
      pair.className = "acl-port-pair";

      const startLabel = document.createElement("label");
      startLabel.className = "acl-port-input";
      const startCaption = document.createElement("span");
      startCaption.textContent = "Start";
      const startInput = document.createElement("input");
      startInput.type = "number";
      startInput.min = "1";
      startInput.max = "65535";
      startInput.className = startClass;
      startInput.value = start || "";
      startInput.placeholder = "any";
      startLabel.append(startCaption, startInput);

      const endLabel = document.createElement("label");
      endLabel.className = "acl-port-input";
      const endCaption = document.createElement("span");
      endCaption.textContent = "End";
      const endInput = document.createElement("input");
      endInput.type = "number";
      endInput.min = "1";
      endInput.max = "65535";
      endInput.className = endClass;
      endInput.value = end || "";
      endInput.placeholder = "any";
      endLabel.append(endCaption, endInput);

      pair.append(startLabel, endLabel);
      icon.append(text, pair);
      return icon;
    };

    fields.append(
      appendSelect("Action", "acl-rule-action", [["permit", "Permit"], ["deny", "Deny"]], rule.action || "permit"),
      appendSelect(
        "Protocol",
        "acl-rule-protocol",
        [
          ["any", "Any"],
          ["tcp", "TCP"],
          ["udp", "UDP"],
          ["icmp", "ICMP"],
          ["icmpv6", "ICMPv6"],
        ],
        rule.protocol || "any"
      ),
      appendInput("Source CIDR", "acl-rule-source", rule.source || "0.0.0.0/0", "0.0.0.0/0"),
      appendInput("Destination CIDR", "acl-rule-destination", rule.destination || "0.0.0.0/0", "0.0.0.0/0"),
      appendInput("Description (optional)", "acl-rule-description", rule.description || "", "Why this rule exists"),
      appendPorts("Source ports", "acl-rule-source-start", "acl-rule-source-end", rule.source_port_start, rule.source_port_end),
      appendPorts("Destination ports", "acl-rule-destination-start", "acl-rule-destination-end", rule.destination_port_start, rule.destination_port_end)
    );

    const protocolSelect = fields.querySelector(".acl-rule-protocol");
    const togglePorts = () => {
      const enabled = protocolSelect.value === "tcp" || protocolSelect.value === "udp";
      for (const input of fields.querySelectorAll(".acl-port-pair input")) {
        input.disabled = !enabled;
      }
    };
    protocolSelect.addEventListener("change", () => {
      togglePorts();
      renderACLPreview();
    });
    togglePorts();
    wrapper.append(head, fields);
    els.aclRuleRows.append(wrapper);
    renumberACLRules();
    renderACLPreview();
  }

  function renderACLPreview() {
    const preview = {
      name: els.aclName.value.trim(),
      title: els.aclTitle.value.trim(),
      description: els.aclDescription.value.trim(),
      rules: readACLRules(),
    };
    els.aclPreview.textContent = formatACLText(preview);
  }

  function openCreateACL() {
    if (!vppReady) {
      showToast("ACL changes are blocked until FD.io VPP is ready.", true);
      return;
    }
    editingACLID = null;
    els.aclForm.reset();
    els.aclID.value = "";
    els.aclRuleRows.replaceChildren();
    addACLRule({ action: "permit", protocol: "any", source: "0.0.0.0/0", destination: "0.0.0.0/0" });
    els.aclDialogEyebrow.textContent = "New ACL";
    els.aclDialogTitle.textContent = "Define ACL";
    els.saveACL.textContent = "Save ACL";
    els.aclFormError.hidden = true;
    els.aclFormError.textContent = "";
    els.aclDialog.showModal();
    els.aclName.focus();
    renderACLPreview();
  }

  function openEditACL(acl) {
    editingACLID = acl.id;
    els.aclForm.reset();
    els.aclID.value = String(acl.id);
    els.aclName.value = acl.name;
    els.aclTitle.value = acl.title || "";
    els.aclDescription.value = acl.description || "";
    els.aclRuleRows.replaceChildren();
    const rules = Array.isArray(acl.rules) ? acl.rules : [];
    for (const rule of rules) {
      addACLRule(rule);
    }
    if (rules.length === 0) {
      addACLRule();
    }
    els.aclDialogEyebrow.textContent = "Edit ACL";
    els.aclDialogTitle.textContent = `ACL · ${acl.name}`;
    els.saveACL.textContent = "Save ACL";
    els.aclFormError.hidden = true;
    els.aclFormError.textContent = "";
    els.aclDialog.showModal();
    els.aclName.focus();
    renderACLPreview();
  }

  function populateInterfaceACLSelect(select, selected) {
    select.replaceChildren();
    const none = document.createElement("option");
    none.value = "";
    none.textContent = "None";
    select.append(none);
    for (const acl of acls) {
      const option = document.createElement("option");
      option.value = acl.name;
      option.textContent = acl.title ? `${acl.name} — ${acl.title}` : acl.name;
      select.append(option);
    }
    select.value = selected || "";
  }

  function renderInterfaceACLPreview() {
    const ingress = aclByName(els.interfaceIngressACL.value);
    const egress = aclByName(els.interfaceEgressACL.value);
    const sections = [];
    sections.push(ingress ? formatACLText(ingress, "From interface ACL") : "From interface ACL: none");
    sections.push(egress ? formatACLText(egress, "To interface ACL") : "To interface ACL: none");
    els.interfaceACLPreview.textContent = sections.join("\n\n");
  }

  function openInterfaceACLs(iface) {
    if (!vppReady) {
      showToast("ACL changes are blocked until FD.io VPP is ready.", true);
      return;
    }
    assigningInterfaceACLID = iface.id;
    els.interfaceACLForm.reset();
    els.interfaceACLTitle.textContent = `ACLs · ${iface.name}`;
    els.interfaceACLLede.textContent = `${iface.network_name} · ${iface.mac}`;
    populateInterfaceACLSelect(els.interfaceIngressACL, iface.ingress_acl_name || "");
    populateInterfaceACLSelect(els.interfaceEgressACL, iface.egress_acl_name || "");
    els.interfaceACLFormError.hidden = true;
    els.interfaceACLFormError.textContent = "";
    els.interfaceACLDialog.showModal();
    renderInterfaceACLPreview();
  }

  function upsertACL(acl) {
    const index = acls.findIndex((item) => item.id === acl.id);
    if (index === -1) {
      acls.push(acl);
    } else {
      acls[index] = acl;
    }
    sortACLs();
    renderACLs();
    renderNetworks();
  }

  function setSavingACL(value, label) {
    savingACL = value;
    els.saveACL.disabled = value;
    els.cancelACLDialog.disabled = value;
    els.closeACLDialog.disabled = value;
    els.saveACL.textContent = value ? label : "Save ACL";
  }

  async function deleteACL(acl) {
    if (!window.confirm(`Delete ACL "${acl.name}"?`)) {
      return;
    }
    try {
      await api(`/api/acls/${acl.id}`, { method: "DELETE" });
      acls = acls.filter((item) => item.id !== acl.id);
      renderACLs();
      renderNetworks();
      showToast(`Deleted ACL ${acl.name}`);
    } catch (err) {
      showACLBanner(`Could not delete ${acl.name}: ${err.message}`);
      showToast(`Could not delete ACL ${acl.name}: ${err.message}`, true);
    }
  }

  function setSavingInterfaceACL(value, label) {
    savingInterfaceACL = value;
    els.saveInterfaceACL.disabled = value;
    els.cancelInterfaceACL.disabled = value;
    els.closeInterfaceACLDialog.disabled = value;
    els.saveInterfaceACL.textContent = value ? label : "Apply ACLs";
  }

  function renderVNFs() {
    const enabled = vnfs.filter((vnf) => vnf.enabled).length;
    els.vnfTabCount.textContent = String(vnfs.length);
    els.vnfSummary.textContent =
      vnfs.length === 0
        ? "No network functions registered"
        : `${vnfs.length} VNF${vnfs.length === 1 ? "" : "s"} · ${enabled} enabled · ${vnfs.reduce((sum, vnf) => sum + (vnf.lease_count || 0), 0)} active leases`;

    els.vnfTable.hidden = vnfs.length === 0;
    els.vnfEmptyState.hidden = vnfs.length !== 0;
    els.vnfRows.replaceChildren();
    for (const vnf of vnfs) {
      els.vnfRows.append(renderVNFRow(vnf));
    }
  }

  function renderVNFRow(vnf) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("span");
    name.className = "vm-name";
    name.textContent = vnf.name;
    nameCell.append(name);
    if (vnf.type === "nat44" && vnf.config?.outside_interface) {
      const outside = document.createElement("span");
      outside.className = "vm-description";
      outside.textContent = `outside ${vnf.config.outside_interface}`;
      nameCell.append(outside);
    } else if (vnf.tap_host_name) {
      const tap = document.createElement("span");
      tap.className = "vm-description";
      tap.textContent = vnf.tap_host_name;
      nameCell.append(tap);
    }

    const typeCell = document.createElement("td");
    typeCell.className = "mono";
    typeCell.textContent =
      vnf.type === "nat44" ? "NAT44" : vnf.type === "dhcpv6" ? "DHCPv6" : "DHCPv4";

    const networkCell = document.createElement("td");
    networkCell.className = "mono";
    networkCell.textContent = networkByID(vnf.network_id)?.name || `network ${vnf.network_id}`;

    const statusCell = document.createElement("td");
    const status = document.createElement("span");
    status.className = "status-badge";
    status.dataset.status = vnf.status || "disabled";
    status.textContent = vnf.status || "disabled";
    if (vnf.status_detail) {
      status.title = vnf.status_detail;
    }
    statusCell.append(status);

    const leaseCell = document.createElement("td");
    leaseCell.className = vnf.lease_count && vnf.type !== "nat44" ? "mono" : "mono muted";
    leaseCell.textContent = vnf.type === "nat44" ? "—" : String(vnf.lease_count || 0);

    const updatedCell = document.createElement("td");
    updatedCell.className = "updated hide-sm";
    updatedCell.textContent = formatDate(vnf.updated_at);

    const actionsCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "actions";

    const leases = document.createElement("button");
    leases.type = "button";
    leases.className = "row-action";
    leases.dataset.action = vnf.type === "nat44" ? "vnf-sessions" : "vnf-leases";
    leases.dataset.id = String(vnf.id);
    leases.textContent = vnf.type === "nat44" ? "Sessions" : "Leases";

    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "row-action";
    edit.dataset.action = "edit-vnf";
    edit.dataset.id = String(vnf.id);
    edit.textContent = "Edit";

    const restart = document.createElement("button");
    restart.type = "button";
    restart.className = "row-action";
    restart.dataset.action = "restart-vnf";
    restart.dataset.id = String(vnf.id);
    restart.textContent = vnf.enabled ? "Restart" : "Start";
    if (!vppReady) {
      restart.disabled = true;
      restart.title = "VPP must be ready to start a VNF.";
    }

    const del = document.createElement("button");
    del.type = "button";
    del.className = "row-action danger";
    del.dataset.action = "delete-vnf";
    del.dataset.id = String(vnf.id);
    del.textContent = "Delete";

    actions.append(leases, edit, restart, del);
    actionsCell.append(actions);
    row.append(nameCell, typeCell, networkCell, statusCell, leaseCell, updatedCell, actionsCell);
    return row;
  }

  function renderInterfaceRow(iface) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    const name = document.createElement("span");
    name.className = "vm-name";
    name.textContent = iface.name;
    nameCell.append(name);
    if (iface.socket_path) {
      const socket = document.createElement("span");
      socket.className = "vm-description";
      socket.textContent = iface.socket_path;
      socket.title = iface.socket_path;
      nameCell.append(socket);
    }

    const networkCell = document.createElement("td");
    networkCell.className = "mono";
    networkCell.textContent = iface.network_name || "—";

    const macCell = document.createElement("td");
    macCell.className = "mono";
    macCell.textContent = iface.mac || "—";

    const owner = iface.attached_vm_id ? vmByID(iface.attached_vm_id) : null;
    const attached = Boolean(iface.attached_vm_id);
    const attachedCell = document.createElement("td");
    attachedCell.className = attached ? "mono" : "mono muted";
    attachedCell.textContent = owner ? owner.name : iface.attached_vm_name || "unattached";

    const aclCell = document.createElement("td");
    aclCell.className = (iface.ingress_acl_name || iface.egress_acl_name) ? "mono" : "mono muted";
    const aclParts = [];
    if (iface.ingress_acl_name) aclParts.push(`from: ${iface.ingress_acl_name}`);
    if (iface.egress_acl_name) aclParts.push(`to: ${iface.egress_acl_name}`);
    aclCell.textContent = aclParts.length ? aclParts.join(" · ") : "none";

    const updatedCell = document.createElement("td");
    updatedCell.className = "updated hide-sm";
    updatedCell.textContent = formatDate(iface.updated_at);

    const actionsCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "actions";

    const attach = document.createElement("button");
    attach.type = "button";
    attach.className = "row-action";
    attach.dataset.action = "attach-interface";
    attach.dataset.id = String(iface.id);
    attach.textContent = "Attach";
    attach.setAttribute("aria-label", `Attach ${iface.name} to a VM`);

    const detach = document.createElement("button");
    detach.type = "button";
    detach.className = "row-action";
    detach.dataset.action = "detach-interface";
    detach.dataset.id = String(iface.id);
    detach.textContent = "Detach";
    detach.setAttribute("aria-label", `Detach ${iface.name}`);
    if (!attached) {
      detach.hidden = true;
    } else if (owner && (owner.status === "running" || owner.status === "starting")) {
      detach.disabled = true;
      detach.title = "Stop the VM before detaching its interface.";
    }

    const del = document.createElement("button");
    del.type = "button";
    del.className = "row-action danger";
    del.dataset.action = "delete-interface";
    del.dataset.id = String(iface.id);
    del.textContent = "Delete";
    del.setAttribute("aria-label", `Delete interface ${iface.name}`);
    if (attached) {
      del.disabled = true;
      del.title = "Detach the interface from its VM first.";
    } else if (!vppReady) {
      del.disabled = true;
      del.title = "VPP must be ready to delete a vhost-user interface.";
    }

    const aclButton = document.createElement("button");
    aclButton.type = "button";
    aclButton.className = "row-action";
    aclButton.dataset.action = "interface-acls";
    aclButton.dataset.id = String(iface.id);
    aclButton.textContent = "ACLs";
    aclButton.setAttribute("aria-label", `Assign ACLs to ${iface.name}`);
    if (!vppReady) {
      aclButton.disabled = true;
      aclButton.title = "VPP must be ready to apply interface ACLs.";
    }

    actions.append(attach, detach, aclButton, del);
    actionsCell.append(actions);
    row.append(nameCell, networkCell, macCell, attachedCell, aclCell, updatedCell, actionsCell);
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
    els.refreshDisks.disabled = true;
    const previousLabel = els.refreshDisks.textContent;
    els.refreshDisks.textContent = "Refreshing…";

    try {
      const data = await api("/api/disks");
      disks = Array.isArray(data) ? data : [];
      sortDisks();
      render();
      if (els.dialog.open) {
        renderVMDiskPicker(currentDialogVM());
      }
      hideDiskBanner();
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      showDiskBanner(`Could not load disks: ${err.message}`);
      throw err;
    } finally {
      els.refreshDisks.disabled = false;
      els.refreshDisks.textContent = previousLabel;
    }
  }

  async function loadStorageBackends() {
    els.refreshStorageBackends.disabled = true;
    const previousLabel = els.refreshStorageBackends.textContent;
    els.refreshStorageBackends.textContent = "Refreshing…";
    try {
      const data = await api("/api/storage-backends");
      storageBackends = Array.isArray(data) ? data : [];
      storageBackends.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
      render();
      hideStorageBackendBanner();
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      showStorageBackendBanner(`Could not load storage backends: ${err.message}`);
      throw err;
    } finally {
      els.refreshStorageBackends.disabled = false;
      els.refreshStorageBackends.textContent = previousLabel;
    }
  }

  async function loadMedia() {
    els.refreshMedia.disabled = true;
    const previousLabel = els.refreshMedia.textContent;
    els.refreshMedia.textContent = "Refreshing…";

    try {
      const data = await api("/api/media");
      media = Array.isArray(data) ? data : [];
      sortMedia();
      render();
      hideMediaBanner();
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      showMediaBanner(`Could not load media: ${err.message}`);
      throw err;
    } finally {
      els.refreshMedia.disabled = false;
      els.refreshMedia.textContent = previousLabel;
    }
  }

  async function loadNetworks() {
    els.refreshNetworks.disabled = true;
    const previousLabel = els.refreshNetworks.textContent;
    els.refreshNetworks.textContent = "Refreshing…";

    try {
      const data = await api("/api/networks");
      networks = Array.isArray(data) ? data : [];
      sortNetworks();
      render();
      hideNetworkBanner();
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      showNetworkBanner(`Could not load networks: ${err.message}`);
      throw err;
    } finally {
      els.refreshNetworks.disabled = false;
      els.refreshNetworks.textContent = previousLabel;
    }
  }

  async function loadVNFs() {
    els.refreshVNFs.disabled = true;
    const previousLabel = els.refreshVNFs.textContent;
    els.refreshVNFs.textContent = "Refreshing…";

    try {
      const data = await api("/api/vnfs");
      vnfs = Array.isArray(data) ? data : [];
      sortVNFs();
      render();
      hideVNFBanner();
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      showVNFBanner(`Could not load VNFs: ${err.message}`);
      throw err;
    } finally {
      els.refreshVNFs.disabled = false;
      els.refreshVNFs.textContent = previousLabel;
    }
  }

  async function loadACLs() {
    els.refreshACLs.disabled = true;
    const previousLabel = els.refreshACLs.textContent;
    els.refreshACLs.textContent = "Refreshing…";
    try {
      const data = await api("/api/acls");
      acls = Array.isArray(data) ? data : [];
      renderACLs();
      hideACLBanner();
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      showACLBanner(`Could not load ACLs: ${err.message}`);
      throw err;
    } finally {
      els.refreshACLs.disabled = false;
      els.refreshACLs.textContent = previousLabel;
    }
  }

  async function loadInterfaces() {
    try {
      const data = await api("/api/interfaces");
      interfaces = Array.isArray(data) ? data : [];
      sortInterfaces();
      render();
      if (els.dialog.open) {
        renderVMInterfacePicker(currentDialogVM());
      }
      hideInterfaceBanner();
      setAPIState("online");
    } catch (err) {
      setAPIState("offline");
      showInterfaceBanner(`Could not load virtual interfaces: ${err.message}`);
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
    updateDiskActionState();
  }

  function setVPPReady(ready) {
    if (vppReady === ready) {
      return;
    }
    vppReady = ready;
    updateNetworkActionState();
    renderNetworks();
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
    vnfs: els.newVNF,
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
      CREATE_BUTTONS[key].hidden = !selected;
    }

    if (updateHash) {
      window.history.replaceState(null, "", `#${name}`);
    }

    const meta = tabMeta[name];
    els.heroEyebrow.textContent = meta.eyebrow;
    els.heroTitle.textContent = meta.title;
    els.heroLede.textContent = meta.lede;
    updateVMActionState();
    updateNetworkActionState();

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
        "Create a Nerd Virtual Interface on the Network tab first.";
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

  function populateDiskBackendOptions(selected = "") {
    if (!els.diskBackend) {
      return;
    }
    els.diskBackend.replaceChildren();
    if (storageBackends.length === 0) {
      const option = document.createElement("option");
      option.value = "";
      option.textContent = "No storage backends";
      els.diskBackend.append(option);
      els.diskBackend.disabled = true;
      return;
    }
    els.diskBackend.disabled = false;
    for (const backend of storageBackends) {
      const option = document.createElement("option");
      option.value = String(backend.id);
      option.textContent = `${backend.name} · ${backend.driver === "lvm-thin" ? "LVM thin" : backend.driver}`;
      els.diskBackend.append(option);
    }
    const fallback = storageBackends.find((backend) => backend.name === "default") || storageBackends[0];
    els.diskBackend.value = selected && storageBackends.some((backend) => String(backend.id) === selected)
      ? selected
      : String(fallback.id);
  }

  function openCreateStorageBackend() {
    editingStorageBackendID = null;
    els.storageBackendForm.reset();
    els.storageBackendID.value = "";
    els.storageBackendDriver.value = "lvm-thin";
    els.storageBackendDialogEyebrow.textContent = "New backend";
    els.storageBackendDialogTitle.textContent = "Register storage backend";
    els.saveStorageBackend.textContent = "Save backend";
    els.storageBackendFormError.hidden = true;
    els.storageBackendFormError.textContent = "";
    els.storageBackendDialog.showModal();
    els.storageBackendName.focus();
  }

  function openEditStorageBackend(backend) {
    editingStorageBackendID = backend.id;
    els.storageBackendForm.reset();
    els.storageBackendID.value = String(backend.id);
    els.storageBackendName.value = backend.name;
    els.storageBackendTitle.value = backend.title || "";
    els.storageBackendDescription.value = backend.description || "";
    els.storageBackendDriver.value = backend.driver;
    els.storageBackendVG.value = backend.config?.vg || "";
    els.storageBackendPool.value = backend.config?.thin_pool || "";
    els.storageBackendDialogEyebrow.textContent = "Edit backend";
    els.storageBackendDialogTitle.textContent = `Storage backend · ${backend.name}`;
    els.saveStorageBackend.textContent = "Save backend";
    els.storageBackendFormError.hidden = true;
    els.storageBackendFormError.textContent = "";
    els.storageBackendDialog.showModal();
    els.storageBackendName.focus();
  }

  function setSavingStorageBackend(value, label) {
    savingStorageBackend = value;
    els.saveStorageBackend.disabled = value;
    els.cancelStorageBackendDialog.disabled = value;
    els.closeStorageBackendDialog.disabled = value;
    els.saveStorageBackend.textContent = value ? label : "Save backend";
  }

  async function deleteStorageBackend(backend) {
    if (!window.confirm(`Delete storage backend "${backend.name}"?`)) {
      return;
    }
    try {
      await api(`/api/storage-backends/${backend.id}`, { method: "DELETE" });
      storageBackends = storageBackends.filter((item) => item.id !== backend.id);
      render();
      showToast(`Deleted storage backend ${backend.name}`);
    } catch (err) {
      showStorageBackendBanner(`Could not delete ${backend.name}: ${err.message}`);
      showToast(`Could not delete backend ${backend.name}: ${err.message}`, true);
    }
  }

  function openImportDisks(backend) {
    if (backend.driver !== "lvm-thin" || !backend.config?.vg || !backend.config?.thin_pool) {
      showToast("Import is available for configured LVM thin backends.", true);
      return;
    }
    importingBackendID = backend.id;
    importDiskItems = [];
    els.importDiskLede.textContent =
      `${backend.name} · ${backend.config.vg}/${backend.config.thin_pool} — ` +
      "select existing thin volumes to register as disks. Importing does not modify LVM, " +
      "but deleting an imported disk later removes its volume.";
    els.importDiskErrorBanner.hidden = true;
    els.importDiskErrorBanner.textContent = "";
    els.importDiskList.replaceChildren();
    els.importDiskEmpty.hidden = true;
    els.importDisks.disabled = true;
    els.importDisks.textContent = "Import selected";
    els.importDiskDialog.showModal();
    loadImportableDisks();
  }

  async function loadImportableDisks() {
    if (importingBackendID === null) {
      return;
    }
    const backendID = importingBackendID;
    const loadID = ++importLoadID;
    els.importDiskErrorBanner.hidden = true;
    els.importDiskErrorBanner.textContent = "";
    els.refreshImportDisks.disabled = true;
    els.importDiskList.replaceChildren();
    els.importDiskEmpty.hidden = true;

    const loading = document.createElement("p");
    loading.className = "import-disk-meta";
    loading.textContent = "Loading existing thin volumes…";
    els.importDiskList.append(loading);
    updateImportDiskActionState();

    try {
      const data = await api(`/api/storage-backends/${backendID}/importable-disks`);
      if (importingBackendID !== backendID || loadID !== importLoadID) {
        return;
      }
      importDiskItems = Array.isArray(data) ? data : [];
      setAPIState("online");
      renderImportDisks();
    } catch (err) {
      if (importingBackendID !== backendID || loadID !== importLoadID) {
        return;
      }
      els.importDiskList.replaceChildren();
      els.importDiskEmpty.hidden = true;
      els.importDiskErrorBanner.textContent = `Could not list existing disks: ${err.message}`;
      els.importDiskErrorBanner.hidden = false;
      if (!err.status) {
        setAPIState("offline");
      }
      updateImportDiskActionState();
    } finally {
      if (importingBackendID === backendID && loadID === importLoadID) {
        els.refreshImportDisks.disabled = false;
      }
    }
  }

  function renderImportDisks() {
    els.importDiskList.replaceChildren();
    els.importDiskEmpty.hidden = importDiskItems.length !== 0;
    if (importDiskItems.length === 0) {
      updateImportDiskActionState();
      return;
    }

    for (const item of importDiskItems) {
      const row = document.createElement("label");
      row.className = item.registered ? "import-disk-row imported" : "import-disk-row";

      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.value = item.name;
      checkbox.disabled = Boolean(item.registered) || savingImport;
      checkbox.setAttribute("aria-label", `Import ${item.name}`);

      const copy = document.createElement("span");
      copy.className = "import-disk-copy";
      const name = document.createElement("span");
      name.className = "import-disk-name";
      name.textContent = item.name;
      const meta = document.createElement("span");
      meta.className = "import-disk-meta";
      const parts = [`${numberFormat.format(item.size_gb)} GB`];
      if (item.registered) {
        parts.push(
          item.attached_vm_name
            ? `already imported · attached to ${item.attached_vm_name}`
            : "already imported"
        );
        if (item.storage_backend_name) {
          parts.push(item.storage_backend_name);
        }
      } else {
        parts.push("available to import");
      }
      meta.textContent = parts.join(" · ");
      copy.append(name, meta);

      const badge = document.createElement("span");
      badge.className = "import-disk-badge";
      badge.textContent = item.registered ? "imported" : "new";

      row.append(checkbox, copy, badge);
      els.importDiskList.append(row);
    }
    updateImportDiskActionState();
  }

  function updateImportDiskActionState() {
    const selected = els.importDiskList.querySelectorAll(
      'input[type="checkbox"]:checked'
    ).length;
    if (savingImport) {
      els.importDisks.disabled = true;
      els.importDisks.textContent = "Importing…";
      return;
    }
    els.importDisks.disabled = selected === 0;
    els.importDisks.textContent =
      selected > 0 ? `Import selected (${selected})` : "Import selected";
  }

  function setSavingImport(value) {
    savingImport = value;
    els.refreshImportDisks.disabled = value;
    els.cancelImportDiskDialog.disabled = value;
    els.closeImportDiskDialog.disabled = value;
    for (const checkbox of els.importDiskList.querySelectorAll('input[type="checkbox"]')) {
      checkbox.disabled = value || checkbox.closest(".imported") !== null;
    }
    updateImportDiskActionState();
  }

  function closeImportDisks() {
    if (savingImport) {
      return;
    }
    els.importDiskDialog.close();
  }

  async function importSelectedDisks() {
    if (importingBackendID === null || savingImport) {
      return;
    }
    const names = Array.from(
      els.importDiskList.querySelectorAll('input[type="checkbox"]:checked')
    ).map((checkbox) => checkbox.value);
    if (names.length === 0) {
      return;
    }

    const backendID = importingBackendID;
    els.importDiskErrorBanner.hidden = true;
    els.importDiskErrorBanner.textContent = "";
    setSavingImport(true);

    try {
      const imported = await api(`/api/storage-backends/${backendID}/import-disks`, {
        method: "POST",
        body: JSON.stringify({ names }),
      });
      for (const disk of imported) {
        upsertDisk(disk);
      }
      setAPIState("online");
      els.importDiskDialog.close();
      loadStorageBackends().catch(() => {});
      showToast(`Imported ${imported.length} disk${imported.length === 1 ? "" : "s"}`);
    } catch (err) {
      els.importDiskErrorBanner.textContent = `Could not import disks: ${err.message}`;
      els.importDiskErrorBanner.hidden = false;
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingImport(false);
    }
  }

  function openCreateDisk() {
    if (storageBackends.length === 0) {
      showToast("Register a storage backend before creating a disk.", true);
      return;
    }

    els.diskForm.reset();
    populateDiskBackendOptions();
    els.diskDialogEyebrow.textContent = "New disk";
    els.diskDialogTitle.textContent = "Create disk";
    els.diskFormError.hidden = true;
    els.diskFormError.textContent = "";
    els.diskDialog.showModal();
    els.diskName.focus();
  }

  function setSavingDisk(value, label) {
    savingDisk = value;
    els.saveDisk.disabled = value;
    els.cancelDiskDialog.disabled = value;
    els.closeDiskDialog.disabled = value;
    els.saveDisk.textContent = value ? label : "Create disk";
  }

  function openCreateMedia() {
    els.mediaForm.reset();
    els.mediaDialogEyebrow.textContent = "New media";
    els.mediaDialogTitle.textContent = "Add installation media";
    els.mediaFormError.hidden = true;
    els.mediaFormError.textContent = "";
    els.mediaDialog.showModal();
    els.mediaName.focus();
  }

  function setSavingMedia(value, label) {
    savingMedia = value;
    els.saveMedia.disabled = value;
    els.cancelMediaDialog.disabled = value;
    els.closeMediaDialog.disabled = value;
    els.saveMedia.textContent = value ? label : "Add media";
  }

  async function deleteMedia(item) {
    const users = mediaUsers(item.id);
    if (users.length > 0) {
      showToast(
        `Clear boot media on ${users.map((vm) => vm.name).join(", ")} first.`,
        true
      );
      return;
    }

    const confirmed = window.confirm(
      `Remove media "${item.name}"?\n\nThis removes the inventory entry only. The ISO file is not deleted.`
    );
    if (!confirmed) {
      return;
    }

    try {
      await api(`/api/media/${item.id}`, { method: "DELETE" });
      media = media.filter((entry) => entry.id !== item.id);
      render();
      hideMediaBanner();
      showToast(`Removed media ${item.name}`);
    } catch (err) {
      showMediaBanner(`Could not remove ${item.name}: ${err.message}`);
    }
  }

  function openCreateNetwork() {
    if (!vppReady) {
      showToast("Network creation is blocked until FD.io VPP is ready.", true);
      return;
    }
    els.networkForm.reset();
    els.networkFormError.hidden = true;
    els.networkFormError.textContent = "";
    els.networkDialog.showModal();
    els.networkName.focus();
  }

  function setSavingNetwork(value, label) {
    savingNetwork = value;
    els.saveNetwork.disabled = value;
    els.cancelNetworkDialog.disabled = value;
    els.closeNetworkDialog.disabled = value;
    els.saveNetwork.textContent = value ? label : "Create network";
  }

  function populateVNFNetworkOptions(selectedID) {
    els.vnfNetwork.replaceChildren();
    if (networks.length === 0) {
      const option = document.createElement("option");
      option.value = "";
      option.textContent = "No networks available";
      els.vnfNetwork.append(option);
      els.vnfNetwork.disabled = true;
      return;
    }

    els.vnfNetwork.disabled = false;
    for (const network of networks) {
      const option = document.createElement("option");
      option.value = String(network.id);
      option.textContent = `${network.name} · bd ${network.bridge_domain_id}`;
      els.vnfNetwork.append(option);
    }
    if (selectedID !== null && selectedID !== undefined) {
      els.vnfNetwork.value = String(selectedID);
    }
  }

  function setVNFTypeHint() {
    const type = els.vnfType.value;
    const ipv6 = type === "dhcpv6";
    const nat44 = type === "nat44";
    els.vnfTypeHint.textContent = nat44
      ? "IPv4 NAT44 gateway. A dedicated network BVI owns the gateway; the outside NIC is taken over by VPP."
      : ipv6
        ? "IPv6 address pool with stateful IA_NA leases on a VPP tap."
        : "IPv4 address pool served on a VPP tap.";
    els.vnfGateway.placeholder = nat44
      ? "10.20.0.1/24 (network BVI gateway)"
      : ipv6
        ? "fd00:20::1/64"
        : "10.20.0.1/24";
    els.vnfDNS.placeholder = ipv6 ? "2001:4860:4860::8888" : "1.1.1.1,8.8.8.8";
    els.vnfRangeStart.placeholder = ipv6 ? "fd00:20::100" : "10.20.0.100";
    els.vnfRangeEnd.placeholder = ipv6 ? "fd00:20::200" : "10.20.0.200";
  }

  function setVNFFieldsForType() {
    const nat44 = els.vnfType.value === "nat44";
    els.vnfDHCPFields.hidden = nat44;
    els.vnfNAT44Fields.hidden = !nat44;
    for (const input of els.vnfDHCPFields.querySelectorAll("input, select")) {
      input.disabled = nat44;
    }
    els.vnfRangeStart.required = !nat44;
    els.vnfRangeEnd.required = !nat44;
    for (const input of els.vnfNAT44Fields.querySelectorAll("input, select")) {
      input.disabled = !nat44;
    }
    els.vnfOutsideInterface.required = nat44;
    els.vnfEnabledHint.textContent = nat44
      ? !vppReady
        ? "VPP is not ready. Save disabled and start the NAT44 VNF later."
        : els.vnfEnabled.checked
          ? "Saving takes over the selected outside NIC and flushes its host addresses."
          : "The configuration is saved without taking over the outside NIC."
      : vppReady
        ? "Creates a VPP tap and starts the function immediately."
        : "VPP is not ready. The VNF can be created disabled and started later.";
    updateOutsideInterfaceHint();
  }

  async function loadHostInterfaces() {
    try {
      const data = await api("/api/host/interfaces");
      hostInterfaces = Array.isArray(data) ? data : [];
    } catch (err) {
      hostInterfaces = [];
    }
    populateOutsideInterfaces(els.vnfOutsideInterface.value);
    return hostInterfaces;
  }

  function populateOutsideInterfaces(selected = "") {
    els.vnfOutsideInterface.replaceChildren();
    if (hostInterfaces.length === 0) {
      const option = document.createElement("option");
      option.value = "";
      option.textContent = "No host interfaces found";
      option.disabled = true;
      els.vnfOutsideInterface.append(option);
      els.vnfOutsideInterface.value = "";
      updateOutsideInterfaceHint();
      return;
    }
    const placeholder = document.createElement("option");
    placeholder.value = "";
    placeholder.textContent = "Select a wired physical NIC…";
    placeholder.disabled = true;
    els.vnfOutsideInterface.append(placeholder);
    let selectedPresent = false;
    for (const iface of hostInterfaces) {
      const option = document.createElement("option");
      option.value = iface.name;
      const addresses = Array.isArray(iface.addresses) && iface.addresses.length
        ? ` — ${iface.addresses.join(", ")}`
        : "";
      const virtual = iface.physical === false;
      const wireless = iface.wireless === true;
      const blocked = Boolean(iface.default_route) || virtual || wireless;
      const reason = iface.default_route
        ? " (default route — blocked)"
        : virtual
          ? " (virtual — not selectable)"
          : wireless
            ? " (wireless — not supported)"
            : "";
      option.textContent = `${iface.name}${addresses}${reason}`;
      option.disabled = blocked;
      els.vnfOutsideInterface.append(option);
      if (iface.name === selected) {
        selectedPresent = true;
        if (blocked) {
          const warning = document.createElement("option");
          warning.value = iface.name;
          warning.textContent = `${iface.name} (${iface.default_route ? "default route" : iface.wireless ? "wireless" : "virtual"} — choose another NIC)`;
          warning.disabled = true;
          els.vnfOutsideInterface.append(warning);
        }
      }
    }
    if (selected && !selectedPresent) {
      const option = document.createElement("option");
      option.value = selected;
      option.textContent = `${selected} (not currently present)`;
      els.vnfOutsideInterface.append(option);
    }
    if (selected) {
      els.vnfOutsideInterface.value = selected;
    }
    updateOutsideInterfaceHint();
  }

  function updateOutsideInterfaceHint() {
    if (!els.vnfOutsideInterface) {
      return;
    }
    const iface = hostInterfaces.find((item) => item.name === els.vnfOutsideInterface.value);
    if (els.vnfType.value !== "nat44") {
      return;
    }
    if (iface?.default_route) {
      els.vnfOutsideHint.textContent =
        "This NIC carries the host default route and cannot be used for NAT.";
      return;
    }
    if (iface?.physical === false) {
      els.vnfOutsideHint.textContent =
        "Select a wired physical NIC; virtual and VPP-managed interfaces are not valid outside uplinks.";
      return;
    }
    if (iface?.wireless) {
      els.vnfOutsideHint.textContent =
        "Wireless NICs are not supported as VPP NAT44 outside uplinks; use a wired NIC.";
      return;
    }
    els.vnfOutsideHint.textContent = iface
      ? `VPP will take over ${iface.name} and flush its host addresses.`
      : "The selected NIC will be taken over by VPP; its host addresses are flushed.";
  }

  function applyVNFTypeDefaults() {
    const type = els.vnfType.value;
    const ipv6 = type === "dhcpv6";
    const nat44 = type === "nat44";
    els.vnfGateway.value = nat44
      ? "10.20.0.1/24"
      : ipv6
        ? "fd00:20::1/64"
        : "10.20.0.1/24";
    els.vnfDNS.value = ipv6 ? "2001:4860:4860::8888" : "1.1.1.1";
    els.vnfRangeStart.value = ipv6 ? "fd00:20::100" : "10.20.0.100";
    els.vnfRangeEnd.value = ipv6 ? "fd00:20::200" : "10.20.0.200";
    els.vnfLeaseTime.value = "12h";
    if (nat44) {
      els.vnfOutsideAddress.value = "";
      els.vnfOutsideGateway.value = "";
      els.vnfNAT44Pool.value = "";
      loadHostInterfaces().catch(() => {});
    }
    setVNFFieldsForType();
  }

  function openCreateVNF(networkID = null) {
    if (networks.length === 0) {
      showToast("Create a Nerd's Network before adding a VNF.", true);
      return;
    }
    editingVNFID = null;
    els.vnfForm.reset();
    els.vnfNetwork.disabled = false;
    els.vnfType.disabled = false;
    populateVNFNetworkOptions(networkID ?? networks[0].id);
    els.vnfType.value = "dhcpv4";
    els.vnfEnabled.checked = vppReady;
    els.vnfEnabled.disabled = !vppReady;
    applyVNFTypeDefaults();
    els.vnfDialogTitle.textContent = "Add VNF";
    els.saveVNF.textContent = "Add VNF";
    els.vnfFormError.hidden = true;
    els.vnfFormError.textContent = "";
    els.vnfDialog.showModal();
    els.vnfName.focus();
  }

  function openEditVNF(vnf) {
    editingVNFID = vnf.id;
    els.vnfForm.reset();
    els.vnfID.value = String(vnf.id);
    els.vnfName.value = vnf.name;
    populateVNFNetworkOptions(vnf.network_id);
    els.vnfNetwork.disabled = true;
    els.vnfType.value = vnf.type;
    els.vnfType.disabled = true;
    els.vnfGateway.value = vnf.config?.gateway || "";
    els.vnfDNS.value = vnf.config?.dns || "";
    els.vnfRangeStart.value = vnf.config?.range_start || "";
    els.vnfRangeEnd.value = vnf.config?.range_end || "";
    els.vnfLeaseTime.value = vnf.config?.lease_time || "12h";
    els.vnfOutsideAddress.value = vnf.config?.outside_address || "";
    els.vnfOutsideGateway.value = vnf.config?.outside_gateway || "";
    els.vnfNAT44Pool.value = vnf.config?.nat44_pool || "";
    els.vnfEnabled.checked = Boolean(vnf.enabled);
    els.vnfEnabled.disabled = false;
    setVNFTypeHint();
    setVNFFieldsForType();
    if (vnf.type === "nat44") {
      loadHostInterfaces().then(() => {
        populateOutsideInterfaces(vnf.config?.outside_interface || "");
      }).catch(() => {});
    }
    els.vnfDialogTitle.textContent = "Edit VNF";
    els.saveVNF.textContent = "Save VNF";
    els.vnfFormError.hidden = true;
    els.vnfFormError.textContent = "";
    els.vnfDialog.showModal();
    els.vnfName.focus();
  }

  function setSavingVNF(value, label) {
    savingVNF = value;
    els.saveVNF.disabled = value;
    els.cancelVNFDialog.disabled = value;
    els.closeVNFDialog.disabled = value;
    els.saveVNF.textContent = value ? label : editingVNFID ? "Save VNF" : "Add VNF";
  }

  function openVNFLeases(vnf) {
    leasesVNFID = vnf.id;
    els.vnfLeasesTitle.textContent = `Leases · ${vnf.name}`;
    els.vnfLeasesLede.textContent = `${vnf.type === "dhcpv6" ? "DHCPv6" : "DHCPv4"} · ${networkByID(vnf.network_id)?.name || "network"}`;
    els.vnfLeasesError.hidden = true;
    els.vnfLeasesError.textContent = "";
    els.vnfLeaseRows.replaceChildren();
    els.vnfLeaseTable.hidden = false;
    els.vnfLeaseEmpty.hidden = true;
    els.vnfLeasesDialog.showModal();
    loadVNFLeases().catch(() => {});
  }

  async function loadVNFLeases() {
    if (leasesVNFID === null || loadingVNFLeases) {
      return;
    }
    loadingVNFLeases = true;
    els.refreshVNFLeases.disabled = true;
    const previousLabel = els.refreshVNFLeases.textContent;
    els.refreshVNFLeases.textContent = "Refreshing…";

    try {
      const data = await api(`/api/vnfs/${leasesVNFID}/leases`);
      const leases = Array.isArray(data?.leases) ? data.leases : [];
      els.vnfLeaseRows.replaceChildren();
      els.vnfLeaseTable.hidden = leases.length === 0;
      els.vnfLeaseEmpty.hidden = leases.length !== 0;
      for (const lease of leases) {
        const row = document.createElement("tr");

        const client = document.createElement("td");
        client.className = "mono";
        client.textContent = lease.client_id || "—";

        const address = document.createElement("td");
        address.className = "mono";
        address.textContent = lease.address || "—";

        const hostname = document.createElement("td");
        hostname.textContent = lease.hostname || "—";

        const expires = document.createElement("td");
        expires.textContent = formatDate(lease.expires_at);

        const active = document.createElement("td");
        const badge = document.createElement("span");
        badge.className = "status-badge";
        badge.dataset.status = lease.active ? "running" : "disabled";
        badge.textContent = lease.active ? "active" : "expired";
        active.append(badge);

        row.append(client, address, hostname, expires, active);
        els.vnfLeaseRows.append(row);
      }
      els.vnfLeasesError.hidden = true;
      els.vnfLeasesError.textContent = "";
    } catch (err) {
      els.vnfLeasesError.textContent = `Could not load leases: ${err.message}`;
      els.vnfLeasesError.hidden = false;
    } finally {
      loadingVNFLeases = false;
      els.refreshVNFLeases.disabled = false;
      els.refreshVNFLeases.textContent = previousLabel;
    }
  }

  function openVNFSessions(vnf) {
    sessionsVNFID = vnf.id;
    els.vnfSessionsTitle.textContent = `NAT44 sessions · ${vnf.name}`;
    els.vnfSessionsLede.textContent = `${networkByID(vnf.network_id)?.name || "network"} · outside ${vnf.config?.outside_interface || "—"}`;
    els.vnfSessionsError.hidden = true;
    els.vnfSessionsError.textContent = "";
    els.vnfSessionsText.textContent = "Loading…";
    els.vnfSessionsDialog.showModal();
    loadVNFSessions().catch(() => {});
  }

  async function loadVNFSessions() {
    if (sessionsVNFID === null) {
      return;
    }
    els.refreshVNFSessions.disabled = true;
    const previousLabel = els.refreshVNFSessions.textContent;
    els.refreshVNFSessions.textContent = "Refreshing…";
    try {
      const data = await api(`/api/vnfs/${sessionsVNFID}/sessions`);
      const lines = Array.isArray(data?.sessions) ? data.sessions : [];
      els.vnfSessionsText.textContent = lines.length
        ? lines.join("\n")
        : "No active NAT44 sessions.";
      els.vnfSessionsError.hidden = true;
      els.vnfSessionsError.textContent = "";
    } catch (err) {
      els.vnfSessionsError.textContent = `Could not load NAT44 sessions: ${err.message}`;
      els.vnfSessionsError.hidden = false;
    } finally {
      els.refreshVNFSessions.disabled = false;
      els.refreshVNFSessions.textContent = previousLabel;
    }
  }

  async function deleteVNF(vnf) {
    const confirmed = window.confirm(
      vnf.type === "nat44"
        ? `Delete VNF "${vnf.name}"?\n\nThis stops NAT44 and removes its VPP rules. The network BVI is kept until the network is deleted.`
        : `Delete VNF "${vnf.name}"?\n\nThis stops the function, removes its tap, and deletes its leases.`
    );
    if (!confirmed) {
      return;
    }
    try {
      await api(`/api/vnfs/${vnf.id}`, { method: "DELETE" });
      vnfs = vnfs.filter((item) => item.id !== vnf.id);
      if (leasesVNFID === vnf.id) {
        els.vnfLeasesDialog.close();
        leasesVNFID = null;
      }
      render();
      hideVNFBanner();
      showToast(`Deleted VNF ${vnf.name}`);
    } catch (err) {
      showVNFBanner(`Could not delete ${vnf.name}: ${err.message}`);
    }
  }

  async function restartVNF(vnf) {
    if (!vppReady) {
      showToast("VPP must be ready to start a VNF.", true);
      return;
    }
    try {
      const updated = await api(`/api/vnfs/${vnf.id}/restart`, { method: "POST" });
      upsertVNF(updated);
      hideVNFBanner();
      showToast(`Started VNF ${vnf.name}`);
    } catch (err) {
      showVNFBanner(`Could not start ${vnf.name}: ${err.message}`);
      if (err.status === 503) {
        refreshStatus();
      }
    }
  }

  function populateNetworkOptions(selectedID) {
    els.interfaceNetwork.replaceChildren();

    if (networks.length === 0) {
      const option = document.createElement("option");
      option.value = "";
      option.textContent = "No networks available";
      els.interfaceNetwork.append(option);
      els.interfaceNetwork.disabled = true;
      return;
    }

    els.interfaceNetwork.disabled = false;
    for (const network of networks) {
      const option = document.createElement("option");
      option.value = String(network.id);
      option.textContent = `${network.name} · bd ${network.bridge_domain_id}`;
      els.interfaceNetwork.append(option);
    }
    if (selectedID !== null && selectedID !== undefined) {
      els.interfaceNetwork.value = String(selectedID);
    }
  }

  function populateVMSelect(select, selectedID, noneLabel) {
    select.replaceChildren();

    const none = document.createElement("option");
    none.value = "";
    none.textContent = noneLabel;
    select.append(none);

    for (const vm of vms) {
      const option = document.createElement("option");
      option.value = vm.id;
      const running = vm.status === "running" || vm.status === "starting";
      option.textContent = running ? `${vm.name} (running — stop first)` : vm.name;
      select.append(option);
    }

    select.value =
      selectedID !== null && selectedID !== undefined ? String(selectedID) : "";
  }

  function openCreateInterface() {
    if (!vppReady) {
      showToast("Interface creation is blocked until FD.io VPP is ready.", true);
      return;
    }
    if (networks.length === 0) {
      showToast("Create a Nerd's Network first.", true);
      return;
    }

    els.interfaceForm.reset();
    populateNetworkOptions(null);
    populateVMSelect(els.interfaceVM, null, "Leave unattached");
    els.interfaceFormError.hidden = true;
    els.interfaceFormError.textContent = "";
    els.interfaceDialog.showModal();
    els.interfaceName.focus();
  }

  function setSavingInterface(value, label) {
    savingInterface = value;
    els.saveInterface.disabled = value;
    els.cancelInterfaceDialog.disabled = value;
    els.closeInterfaceDialog.disabled = value;
    els.saveInterface.textContent = value ? label : "Add interface";
  }

  function openAttachInterface(iface) {
    editingInterfaceID = iface.id;
    els.attachForm.reset();
    els.attachInterfaceName.value = iface.name;
    populateVMSelect(
      els.attachVM,
      iface.attached_vm_id || null,
      "Leave unattached"
    );
    els.attachFormError.hidden = true;
    els.attachFormError.textContent = "";
    els.attachDialog.showModal();
    els.attachVM.focus();
  }

  function setSavingAttach(value, label) {
    savingAttach = value;
    els.saveAttach.disabled = value;
    els.cancelAttachDialog.disabled = value;
    els.closeAttachDialog.disabled = value;
    els.saveAttach.textContent = value ? label : "Save attachment";
  }

  async function detachInterface(iface) {
    const owner = iface.attached_vm_id ? vmByID(iface.attached_vm_id) : null;
    if (owner && (owner.status === "running" || owner.status === "starting")) {
      showToast("Stop the VM before detaching its interface.", true);
      return;
    }
    const confirmed = window.confirm(`Detach interface "${iface.name}" from its VM?`);
    if (!confirmed) {
      return;
    }

    try {
      const updated = await api(`/api/interfaces/${iface.id}`, {
        method: "PUT",
        body: JSON.stringify({ attached_vm_id: null }),
      });
      upsertInterface(updated);
      hideInterfaceBanner();
      showToast(`Detached ${iface.name}`);
    } catch (err) {
      showInterfaceBanner(`Could not detach ${iface.name}: ${err.message}`);
    }
  }

  async function deleteNetwork(network) {
    const count = interfaces.filter((iface) => iface.network_id === network.id).length;
    if (count > 0) {
      showToast("Delete the virtual interfaces in this network first.", true);
      return;
    }
    const networkVNFs = vnfsForNetwork(network.id);
    const confirmed = window.confirm(
      `Delete Nerd's Network "${network.name}"?\n\nThis removes the VPP bridge domain and ${networkVNFs.length} VNF(s).`
    );
    if (!confirmed) {
      return;
    }

    try {
      await api(`/api/networks/${network.id}`, { method: "DELETE" });
      networks = networks.filter((item) => item.id !== network.id);
      vnfs = vnfs.filter((vnf) => vnf.network_id !== network.id);
      render();
      hideNetworkBanner();
      showToast(`Deleted network ${network.name}`);
    } catch (err) {
      showNetworkBanner(`Could not delete ${network.name}: ${err.message}`);
    }
  }

  async function deleteInterface(iface) {
    if (iface.attached_vm_id) {
      showToast("Detach the interface from its VM first.", true);
      return;
    }
    const confirmed = window.confirm(
      `Delete virtual interface "${iface.name}"?\n\nThis removes the VPP vhost-user interface and socket.`
    );
    if (!confirmed) {
      return;
    }

    try {
      await api(`/api/interfaces/${iface.id}`, { method: "DELETE" });
      interfaces = interfaces.filter((item) => item.id !== iface.id);
      render();
      hideInterfaceBanner();
      showToast(`Deleted interface ${iface.name}`);
    } catch (err) {
      showInterfaceBanner(`Could not delete ${iface.name}: ${err.message}`);
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

  async function deleteDisk(disk) {
    const owner = diskOwner(disk.id);
    if (owner) {
      showToast(
        `Detach ${disk.name} from ${owner.name} before deleting it.`,
        true
      );
      return;
    }

    const confirmed = window.confirm(
      `Delete disk "${disk.name}"?\n\nThis removes the LVM thin volume and its inventory record.`
    );
    if (!confirmed) {
      return;
    }

    try {
      await api(`/api/disks/${disk.id}`, { method: "DELETE" });
      disks = disks.filter((item) => item.id !== disk.id);
      render();
      loadStorageBackends().catch(() => {});
      hideDiskBanner();
      showToast(`Deleted disk ${disk.name}`);
    } catch (err) {
      showDiskBanner(`Could not delete ${disk.name}: ${err.message}`);
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

  els.storageBackendForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingStorageBackend) {
      return;
    }
    els.storageBackendFormError.hidden = true;
    els.storageBackendFormError.textContent = "";
    if (!els.storageBackendForm.reportValidity()) {
      return;
    }
    const payload = {
      name: els.storageBackendName.value.trim(),
      title: els.storageBackendTitle.value.trim(),
      description: els.storageBackendDescription.value.trim(),
      driver: els.storageBackendDriver.value,
      config: {
        vg: els.storageBackendVG.value.trim(),
        thin_pool: els.storageBackendPool.value.trim(),
      },
    };
    setSavingStorageBackend(true, "Saving…");
    try {
      const backend = await api(
        editingStorageBackendID === null
          ? "/api/storage-backends"
          : `/api/storage-backends/${editingStorageBackendID}`,
        {
          method: editingStorageBackendID === null ? "POST" : "PUT",
          body: JSON.stringify(payload),
        }
      );
      const index = storageBackends.findIndex((item) => item.id === backend.id);
      if (index === -1) storageBackends.push(backend);
      else storageBackends[index] = backend;
      storageBackends.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
      render();
      loadStorageBackends().catch(() => {});
      els.storageBackendDialog.close();
      showToast(editingStorageBackendID === null ? `Added storage backend ${backend.name}` : `Saved storage backend ${backend.name}`);
    } catch (err) {
      els.storageBackendFormError.textContent = err.message;
      els.storageBackendFormError.hidden = false;
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingStorageBackend(false);
    }
  });

  els.diskForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingDisk) {
      return;
    }

    els.diskFormError.hidden = true;
    els.diskFormError.textContent = "";

    if (!els.diskForm.reportValidity()) {
      return;
    }

    const payload = {
      name: els.diskName.value.trim(),
      size_gb: Number(els.diskSize.value),
      storage_backend_id: Number(els.diskBackend.value),
    };

    setSavingDisk(true, "Creating…");

    try {
      const disk = await api("/api/disks", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      upsertDisk(disk);
      setAPIState("online");
      hideDiskBanner();
      els.diskDialog.close();
      loadStorageBackends().catch(() => {});
      showToast(`Created disk ${disk.name}`);
    } catch (err) {
      els.diskFormError.textContent = err.message;
      els.diskFormError.hidden = false;
      if (err.status === 503) {
        refreshStatus();
      }
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingDisk(false);
    }
  });

  els.mediaForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingMedia) {
      return;
    }

    els.mediaFormError.hidden = true;
    els.mediaFormError.textContent = "";

    if (!els.mediaForm.reportValidity()) {
      return;
    }

    const payload = {
      name: els.mediaName.value.trim(),
      path: els.mediaPath.value.trim(),
    };

    setSavingMedia(true, "Adding…");

    try {
      const item = await api("/api/media", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      upsertMedia(item);
      setAPIState("online");
      hideMediaBanner();
      els.mediaDialog.close();
      showToast(`Added media ${item.name}`);
    } catch (err) {
      els.mediaFormError.textContent = err.message;
      els.mediaFormError.hidden = false;
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingMedia(false);
    }
  });

  els.networkForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingNetwork) {
      return;
    }

    els.networkFormError.hidden = true;
    els.networkFormError.textContent = "";

    if (!els.networkForm.reportValidity()) {
      return;
    }

    const payload = { name: els.networkName.value.trim() };
    setSavingNetwork(true, "Creating…");

    try {
      const network = await api("/api/networks", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      upsertNetwork(network);
      setAPIState("online");
      hideNetworkBanner();
      els.networkDialog.close();
      showToast(`Created network ${network.name}`);
    } catch (err) {
      els.networkFormError.textContent = err.message;
      els.networkFormError.hidden = false;
      if (err.status === 503) {
        refreshStatus();
      }
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingNetwork(false);
    }
  });

  els.vnfForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingVNF) {
      return;
    }

    els.vnfFormError.hidden = true;
    els.vnfFormError.textContent = "";

    if (!els.vnfForm.reportValidity()) {
      return;
    }

    const vnfType = els.vnfType.value;
    const nat44 = vnfType === "nat44";
    const config = nat44
      ? {
          gateway: els.vnfGateway.value.trim(),
          outside_interface: els.vnfOutsideInterface.value,
          outside_address: els.vnfOutsideAddress.value.trim(),
          outside_gateway: els.vnfOutsideGateway.value.trim(),
          nat44_pool: els.vnfNAT44Pool.value.trim(),
        }
      : {
          gateway: els.vnfGateway.value.trim(),
          range_start: els.vnfRangeStart.value.trim(),
          range_end: els.vnfRangeEnd.value.trim(),
          dns: els.vnfDNS.value.trim(),
          lease_time: els.vnfLeaseTime.value.trim() || "12h",
        };
    if (!config.gateway) {
      els.vnfFormError.textContent = "Gateway CIDR is required.";
      els.vnfFormError.hidden = false;
      return;
    }
    if (!nat44 && (!config.range_start || !config.range_end)) {
      els.vnfFormError.textContent = "Range start and end are required.";
      els.vnfFormError.hidden = false;
      return;
    }
    if (nat44 && !config.outside_interface) {
      els.vnfFormError.textContent = "Select an outside network interface.";
      els.vnfFormError.hidden = false;
      return;
    }
    if (
      nat44 &&
      els.vnfEnabled.checked &&
      !window.confirm(
        `Take over ${config.outside_interface} for NAT44?\n\nVPP will own this NIC and its host IPv4/IPv6 addresses will be flushed. The host default-route interface is rejected by the server.`
      )
    ) {
      return;
    }

    const payload = {
      name: els.vnfName.value.trim(),
      enabled: els.vnfEnabled.checked,
      config,
    };
    if (editingVNFID === null) {
      payload.network_id = Number(els.vnfNetwork.value);
      payload.type = els.vnfType.value;
    }

    setSavingVNF(true, "Saving…");

    try {
      const vnf = await api(editingVNFID === null ? "/api/vnfs" : `/api/vnfs/${editingVNFID}`, {
        method: editingVNFID === null ? "POST" : "PUT",
        body: JSON.stringify(payload),
      });
      upsertVNF(vnf);
      setAPIState("online");
      hideVNFBanner();
      els.vnfDialog.close();
      if (vnf.type === "nat44") {
        loadVNFs().catch(() => {});
        loadNetworks().catch(() => {});
      }
      showToast(
        editingVNFID === null
          ? `Added VNF ${vnf.name}`
          : `Saved VNF ${vnf.name}`
      );
    } catch (err) {
      els.vnfFormError.textContent = err.message;
      els.vnfFormError.hidden = false;
      if (err.status === 503) {
        refreshStatus();
      }
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingVNF(false);
    }
  });

  els.aclForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingACL) {
      return;
    }
    els.aclFormError.hidden = true;
    els.aclFormError.textContent = "";
    if (!els.aclForm.reportValidity()) {
      return;
    }
    const rules = readACLRules();
    if (rules.length === 0) {
      els.aclFormError.textContent = "Add at least one rule.";
      els.aclFormError.hidden = false;
      return;
    }
    const payload = {
      name: els.aclName.value.trim(),
      title: els.aclTitle.value.trim(),
      description: els.aclDescription.value.trim(),
      rules,
    };
    if (!payload.name) {
      els.aclFormError.textContent = "ACL name is required.";
      els.aclFormError.hidden = false;
      return;
    }
    setSavingACL(true, "Saving…");
    try {
      const acl = await api(editingACLID === null ? "/api/acls" : `/api/acls/${editingACLID}`, {
        method: editingACLID === null ? "POST" : "PUT",
        body: JSON.stringify(payload),
      });
      upsertACL(acl);
      loadInterfaces().catch(() => {});
      hideACLBanner();
      els.aclDialog.close();
      showToast(editingACLID === null ? `Added ACL ${acl.name}` : `Saved ACL ${acl.name}`);
    } catch (err) {
      els.aclFormError.textContent = err.message;
      els.aclFormError.hidden = false;
      if (err.status === 503) {
        refreshStatus();
      }
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingACL(false);
    }
  });

  els.interfaceACLForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingInterfaceACL || assigningInterfaceACLID === null) {
      return;
    }
    els.interfaceACLFormError.hidden = true;
    els.interfaceACLFormError.textContent = "";
    setSavingInterfaceACL(true, "Applying…");
    try {
      const updated = await api(`/api/interfaces/${assigningInterfaceACLID}/acls`, {
        method: "PUT",
        body: JSON.stringify({
          ingress_acl: els.interfaceIngressACL.value,
          egress_acl: els.interfaceEgressACL.value,
        }),
      });
      upsertInterface(updated);
      loadInterfaces().catch(() => {});
      loadVMs().catch(() => {});
      hideInterfaceBanner();
      els.interfaceACLDialog.close();
      showToast(`Applied ACLs to ${updated.name}`);
    } catch (err) {
      els.interfaceACLFormError.textContent = err.message;
      els.interfaceACLFormError.hidden = false;
      if (err.status === 503) {
        refreshStatus();
      }
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingInterfaceACL(false);
    }
  });

  els.interfaceForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingInterface) {
      return;
    }

    els.interfaceFormError.hidden = true;
    els.interfaceFormError.textContent = "";

    if (!els.interfaceForm.reportValidity()) {
      return;
    }

    const payload = {
      name: els.interfaceName.value.trim(),
      network_id: Number(els.interfaceNetwork.value),
    };
    const vmID = els.interfaceVM.value;
    if (vmID) {
      payload.attached_vm_id = vmID;
    }

    setSavingInterface(true, "Creating…");

    try {
      const iface = await api("/api/interfaces", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      upsertInterface(iface);
      setAPIState("online");
      hideInterfaceBanner();
      els.interfaceDialog.close();
      showToast(`Created interface ${iface.name}`);
    } catch (err) {
      els.interfaceFormError.textContent = err.message;
      els.interfaceFormError.hidden = false;
      if (err.status === 503) {
        refreshStatus();
      }
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingInterface(false);
    }
  });

  els.attachForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (savingAttach || editingInterfaceID === null) {
      return;
    }

    els.attachFormError.hidden = true;
    els.attachFormError.textContent = "";

    const vmID = els.attachVM.value;
    setSavingAttach(true, "Saving…");

    try {
      const iface = await api(`/api/interfaces/${editingInterfaceID}`, {
        method: "PUT",
        body: JSON.stringify({ attached_vm_id: vmID || null }),
      });
      upsertInterface(iface);
      setAPIState("online");
      hideInterfaceBanner();
      els.attachDialog.close();
      showToast(
        iface.attached_vm_id
          ? `Attached ${iface.name} to ${iface.attached_vm_name}`
          : `Detached ${iface.name}`
      );
    } catch (err) {
      els.attachFormError.textContent = err.message;
      els.attachFormError.hidden = false;
      if (!err.status) {
        setAPIState("offline");
      }
    } finally {
      setSavingAttach(false);
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

  els.storageBackendRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }
    const backend = storageBackends.find((item) => item.id === Number(button.dataset.id));
    if (!backend) {
      return;
    }
    if (button.dataset.action === "import-storage-backend") {
      openImportDisks(backend);
    } else if (button.dataset.action === "edit-storage-backend") {
      openEditStorageBackend(backend);
    } else if (button.dataset.action === "delete-storage-backend") {
      deleteStorageBackend(backend);
    }
  });

  els.diskRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }

    const disk = disks.find((item) => item.id === Number(button.dataset.id));
    if (!disk) {
      return;
    }

    if (button.dataset.action === "delete-disk") {
      deleteDisk(disk);
    }
  });

  els.mediaRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }

    const item = media.find((entry) => entry.id === Number(button.dataset.id));
    if (!item) {
      return;
    }

    if (button.dataset.action === "delete-media") {
      deleteMedia(item);
    }
  });

  els.networkRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }
    const network = networks.find((item) => item.id === Number(button.dataset.id));
    if (!network) {
      return;
    }
    if (button.dataset.action === "delete-network") {
      deleteNetwork(network);
    } else if (button.dataset.action === "add-vnf") {
      openCreateVNF(network.id);
    }
  });

  els.vnfRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }
    const vnf = vnfByID(Number(button.dataset.id));
    if (!vnf) {
      return;
    }
    if (button.dataset.action === "vnf-leases") {
      openVNFLeases(vnf);
    } else if (button.dataset.action === "vnf-sessions") {
      openVNFSessions(vnf);
    } else if (button.dataset.action === "edit-vnf") {
      openEditVNF(vnf);
    } else if (button.dataset.action === "restart-vnf") {
      restartVNF(vnf);
    } else if (button.dataset.action === "delete-vnf") {
      deleteVNF(vnf);
    }
  });

  els.aclRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }
    const acl = acls.find((item) => item.id === Number(button.dataset.id));
    if (!acl) {
      return;
    }
    if (button.dataset.action === "edit-acl") {
      openEditACL(acl);
    } else if (button.dataset.action === "delete-acl") {
      deleteACL(acl);
    }
  });

  els.interfaceRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) {
      return;
    }
    const iface = interfaces.find((item) => item.id === Number(button.dataset.id));
    if (!iface) {
      return;
    }
    if (button.dataset.action === "attach-interface") {
      openAttachInterface(iface);
    } else if (button.dataset.action === "detach-interface") {
      detachInterface(iface);
    } else if (button.dataset.action === "interface-acls") {
      openInterfaceACLs(iface);
    } else if (button.dataset.action === "delete-interface") {
      deleteInterface(iface);
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
  els.newDisk.addEventListener("click", openCreateDisk);
  els.newStorageBackend.addEventListener("click", openCreateStorageBackend);
  els.emptyCreateStorageBackend.addEventListener("click", openCreateStorageBackend);
  els.refreshStorageBackends.addEventListener("click", () => {
    loadStorageBackends().catch(() => {});
    loadDisks().catch(() => {});
  });
  els.emptyCreateDisk.addEventListener("click", openCreateDisk);
  els.newMedia.addEventListener("click", openCreateMedia);
  els.emptyCreateMedia.addEventListener("click", openCreateMedia);
  els.newNetwork.addEventListener("click", openCreateNetwork);
  els.emptyCreateNetwork.addEventListener("click", openCreateNetwork);
  els.newInterface.addEventListener("click", openCreateInterface);
  els.newACL.addEventListener("click", openCreateACL);
  els.emptyCreateACL.addEventListener("click", openCreateACL);
  els.refreshACLs.addEventListener("click", () => loadACLs().catch(() => {}));
  els.addACLRule.addEventListener("click", () => addACLRule());
  els.aclRuleRows.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-remove-rule]");
    if (!button) {
      return;
    }
    button.closest(".acl-rule")?.remove();
    renumberACLRules();
    renderACLPreview();
  });
  els.aclRuleRows.addEventListener("input", renderACLPreview);
  els.aclRuleRows.addEventListener("change", renderACLPreview);
  els.aclName.addEventListener("input", renderACLPreview);
  els.aclTitle.addEventListener("input", renderACLPreview);
  els.aclDescription.addEventListener("input", renderACLPreview);
  els.interfaceIngressACL.addEventListener("change", renderInterfaceACLPreview);
  els.interfaceEgressACL.addEventListener("change", renderInterfaceACLPreview);
  els.newVNF.addEventListener("click", () => openCreateVNF());
  els.emptyCreateVNF.addEventListener("click", () => openCreateVNF());
  els.vnfType.addEventListener("change", () => {
    if (editingVNFID === null) {
      applyVNFTypeDefaults();
    } else {
      setVNFTypeHint();
      setVNFFieldsForType();
    }
    if (els.vnfType.value === "nat44") {
      loadHostInterfaces().catch(() => {});
    }
  });
  els.vnfOutsideInterface.addEventListener("change", updateOutsideInterfaceHint);
  els.vnfEnabled.addEventListener("change", setVNFFieldsForType);
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
  els.importDiskList.addEventListener("change", updateImportDiskActionState);
  els.importDisks.addEventListener("click", importSelectedDisks);
  els.refreshImportDisks.addEventListener("click", () => {
    loadImportableDisks();
  });
  els.cancelImportDiskDialog.addEventListener("click", closeImportDisks);
  els.closeImportDiskDialog.addEventListener("click", closeImportDisks);
  els.importDiskDialog.addEventListener("click", (event) => {
    if (event.target === els.importDiskDialog) {
      closeImportDisks();
    }
  });
  els.importDiskDialog.addEventListener("cancel", (event) => {
    if (savingImport) {
      event.preventDefault();
    }
  });
  els.importDiskDialog.addEventListener("close", () => {
    importingBackendID = null;
    importDiskItems = [];
  });

  els.refresh.addEventListener("click", () => {
    loadVMs().catch(() => {});
    loadDisks().catch(() => {});
    loadStorageBackends().catch(() => {});
    loadMedia().catch(() => {});
    loadNetworks().catch(() => {});
    loadInterfaces().catch(() => {});
    loadACLs().catch(() => {});
    loadVNFs().catch(() => {});
    refreshStatus();
  });
  els.refreshDisks.addEventListener("click", () => {
    loadDisks().catch(() => {});
    loadStorageBackends().catch(() => {});
    loadVMs().catch(() => {});
    refreshStatus();
  });
  els.refreshMedia.addEventListener("click", () => {
    loadMedia().catch(() => {});
    loadVMs().catch(() => {});
    refreshStatus();
  });
  els.refreshNetworks.addEventListener("click", () => {
    loadNetworks().catch(() => {});
    loadInterfaces().catch(() => {});
    loadACLs().catch(() => {});
    loadVNFs().catch(() => {});
    loadVMs().catch(() => {});
    refreshStatus();
  });
  els.refreshVNFs.addEventListener("click", () => {
    loadVNFs().catch(() => {});
    loadNetworks().catch(() => {});
    refreshStatus();
  });
  els.refreshVNFLeases.addEventListener("click", () => loadVNFLeases().catch(() => {}));
  els.closeVNFLeases.addEventListener("click", () => els.vnfLeasesDialog.close());
  els.closeVNFLeasesDialog.addEventListener("click", () => els.vnfLeasesDialog.close());
  els.refreshVNFSessions.addEventListener("click", () => loadVNFSessions().catch(() => {}));
  els.closeVNFSessions.addEventListener("click", () => els.vnfSessionsDialog.close());
  els.closeVNFSessionsDialog.addEventListener("click", () => els.vnfSessionsDialog.close());
  els.vnfSessionsDialog.addEventListener("click", (event) => {
    if (event.target === els.vnfSessionsDialog) {
      els.vnfSessionsDialog.close();
    }
  });
  els.vnfSessionsDialog.addEventListener("close", () => {
    sessionsVNFID = null;
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

  els.closeStorageBackendDialog.addEventListener("click", () => {
    if (!savingStorageBackend) {
      els.storageBackendDialog.close();
    }
  });
  els.cancelStorageBackendDialog.addEventListener("click", () => {
    if (!savingStorageBackend) {
      els.storageBackendDialog.close();
    }
  });
  els.storageBackendDialog.addEventListener("click", (event) => {
    if (event.target === els.storageBackendDialog && !savingStorageBackend) {
      els.storageBackendDialog.close();
    }
  });
  els.storageBackendDialog.addEventListener("cancel", (event) => {
    if (savingStorageBackend) {
      event.preventDefault();
    }
  });
  els.storageBackendDialog.addEventListener("close", () => {
    editingStorageBackendID = null;
  });

  els.closeDiskDialog.addEventListener("click", () => {
    if (!savingDisk) {
      els.diskDialog.close();
    }
  });
  els.cancelDiskDialog.addEventListener("click", () => {
    if (!savingDisk) {
      els.diskDialog.close();
    }
  });
  els.diskDialog.addEventListener("click", (event) => {
    if (event.target === els.diskDialog && !savingDisk) {
      els.diskDialog.close();
    }
  });
  els.diskDialog.addEventListener("cancel", (event) => {
    if (savingDisk) {
      event.preventDefault();
    }
  });

  els.closeMediaDialog.addEventListener("click", () => {
    if (!savingMedia) {
      els.mediaDialog.close();
    }
  });
  els.cancelMediaDialog.addEventListener("click", () => {
    if (!savingMedia) {
      els.mediaDialog.close();
    }
  });
  els.mediaDialog.addEventListener("click", (event) => {
    if (event.target === els.mediaDialog && !savingMedia) {
      els.mediaDialog.close();
    }
  });
  els.mediaDialog.addEventListener("cancel", (event) => {
    if (savingMedia) {
      event.preventDefault();
    }
  });

  els.closeNetworkDialog.addEventListener("click", () => {
    if (!savingNetwork) {
      els.networkDialog.close();
    }
  });
  els.cancelNetworkDialog.addEventListener("click", () => {
    if (!savingNetwork) {
      els.networkDialog.close();
    }
  });
  els.networkDialog.addEventListener("click", (event) => {
    if (event.target === els.networkDialog && !savingNetwork) {
      els.networkDialog.close();
    }
  });
  els.networkDialog.addEventListener("cancel", (event) => {
    if (savingNetwork) {
      event.preventDefault();
    }
  });

  els.closeVNFDialog.addEventListener("click", () => {
    if (!savingVNF) {
      els.vnfDialog.close();
    }
  });
  els.cancelVNFDialog.addEventListener("click", () => {
    if (!savingVNF) {
      els.vnfDialog.close();
    }
  });
  els.vnfDialog.addEventListener("click", (event) => {
    if (event.target === els.vnfDialog && !savingVNF) {
      els.vnfDialog.close();
    }
  });
  els.vnfDialog.addEventListener("cancel", (event) => {
    if (savingVNF) {
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

  els.vnfLeasesDialog.addEventListener("click", (event) => {
    if (event.target === els.vnfLeasesDialog) {
      els.vnfLeasesDialog.close();
    }
  });
  els.vnfLeasesDialog.addEventListener("cancel", () => {
    leasesVNFID = null;
  });
  els.vnfLeasesDialog.addEventListener("close", () => {
    leasesVNFID = null;
  });

  els.closeACLDialog.addEventListener("click", () => {
    if (!savingACL) {
      els.aclDialog.close();
    }
  });
  els.cancelACLDialog.addEventListener("click", () => {
    if (!savingACL) {
      els.aclDialog.close();
    }
  });
  els.aclDialog.addEventListener("click", (event) => {
    if (event.target === els.aclDialog && !savingACL) {
      els.aclDialog.close();
    }
  });
  els.aclDialog.addEventListener("cancel", (event) => {
    if (savingACL) {
      event.preventDefault();
    }
  });
  els.aclDialog.addEventListener("close", () => {
    editingACLID = null;
  });

  els.closeInterfaceACLDialog.addEventListener("click", () => {
    if (!savingInterfaceACL) {
      els.interfaceACLDialog.close();
    }
  });
  els.cancelInterfaceACL.addEventListener("click", () => {
    if (!savingInterfaceACL) {
      els.interfaceACLDialog.close();
    }
  });
  els.interfaceACLDialog.addEventListener("click", (event) => {
    if (event.target === els.interfaceACLDialog && !savingInterfaceACL) {
      els.interfaceACLDialog.close();
    }
  });
  els.interfaceACLDialog.addEventListener("cancel", (event) => {
    if (savingInterfaceACL) {
      event.preventDefault();
    }
  });
  els.interfaceACLDialog.addEventListener("close", () => {
    assigningInterfaceACLID = null;
  });

  els.closeInterfaceDialog.addEventListener("click", () => {
    if (!savingInterface) {
      els.interfaceDialog.close();
    }
  });
  els.cancelInterfaceDialog.addEventListener("click", () => {
    if (!savingInterface) {
      els.interfaceDialog.close();
    }
  });
  els.interfaceDialog.addEventListener("click", (event) => {
    if (event.target === els.interfaceDialog && !savingInterface) {
      els.interfaceDialog.close();
    }
  });
  els.interfaceDialog.addEventListener("cancel", (event) => {
    if (savingInterface) {
      event.preventDefault();
    }
  });

  els.closeAttachDialog.addEventListener("click", () => {
    if (!savingAttach) {
      els.attachDialog.close();
    }
  });
  els.cancelAttachDialog.addEventListener("click", () => {
    if (!savingAttach) {
      els.attachDialog.close();
    }
  });
  els.attachDialog.addEventListener("click", (event) => {
    if (event.target === els.attachDialog && !savingAttach) {
      els.attachDialog.close();
    }
  });
  els.attachDialog.addEventListener("cancel", (event) => {
    if (savingAttach) {
      event.preventDefault();
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
  Promise.allSettled([loadVMs(), loadDisks(), loadStorageBackends(), loadMedia(), loadNetworks(), loadInterfaces(), loadACLs(), loadVNFs()]);
})();
