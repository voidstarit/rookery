# Revision 6 Remediation Report

*   **Target:** `docs/designs/instance-templates-and-groups.md` (revision 6)
*   **Review baseline:** `final_review_report.md` (revision 5, 9 blockers)
*   **Previous revision:** `/tmp/instance-templates-and-groups.revision5.md`
*   **Direction:** Full lifecycle reuse (selected 2026-11-24)

--------------------------------------------------------------------------------

## Verdict

Revision 6 switches the fleet to the existing LightVM lifecycle. It uses
`createLightVMDefinition`, `StartLightVM`, `StopLightVM`, `DeleteLightVM`,
`StartCoordinator`, `runMu`, `lifecycle_generation`, and string UUIDs. It does
not add a second start path or a force-kill runtime method.

The nine final-review blockers are addressed in the design. A targeted re-review
is still the gate before implementation.

--------------------------------------------------------------------------------

## Nine Blocker Resolution

### 1. Action budget was per group, not per pass

**Resolved.** §4.1/§4.5 define one `fleetMaxActionsPerPass = 10` counter for the
whole `ReconcileFleet` call, a 30-minute pass deadline, and budget charges for
missing-to-provisioning marking, repair, cleanup/removal, stop, restart, and
create. A large group can consume a pass; later groups wait for the next pass.
Tests cover a mixed pass and exactly ten actions across groups.

### 2. A failed repair lost stopped power intent

**Resolved.** Member states are now `provisioning`, `active`, `removing`.
Failed creates and repairs stay `provisioning` with error/backoff; the member row
is never deleted outside scale-in or group deletion. The state diagram and CUJ 8
reflect that. Test: stopped member → out-of-band delete → repair failure → retry
→ still stopped.

### 3. Provisioning + existing LightVM had no recovery; `creating` was invalid

**Resolved.** §4.3 defines the adopt path for a `provisioning` member whose
LightVM already exists: adopt if running, start if stopped/error/starting, stop
if desired stopped, or delete/repair on attachment mismatch. Crash windows after
definition insert, after prepare, and after QEMU start are covered. The design
uses `stopped` for the inserted definition and the valid statuses
`starting`/`running`/`error`; `creating` is explicitly removed.

### 4. Fleet start bypassed lifecycle serialization

**Resolved.** The fleet uses the same `createLightVMDefinition` and internal
start wrapper around the existing lifecycle. `StartLightVM`/`StopLightVM`/
`DeleteLightVM` use `StartCoordinator.Begin`, `Reserve`, `Cancel`, `runMu`, and
`lifecycle_generation`. The fleet wrapper is only group/member state checking;
there is no direct runtime start. Tests cover `/stop` and `/delete` during a
blocked image pull.

### 5. “Cleanup without VPP” was impossible

**Resolved.** The design no longer claims VPP-free interface deletion. Cleanup
and group deletion do not require the fleet epoch, but `DeleteInterface` still
requires VPP; if VPP is down, the member stays `removing` with backoff. Force may
clear ownership and leave the interface in inventory for later reconciliation.
Tests cover scale-in and group deletion with VPP down.

### 6. Store contracts used `int64` LightVM IDs

**Resolved.** All LightVM IDs are `string` UUIDs. `SetGroupMemberPower`,
`GetGroupMemberByLightVMID`, and `ClearLightVMGroupOwner` use strings. The
existing `SetLightVMDesiredState(ctx, id string, state string) (LightVM, error)`
signature is preserved rather than duplicated. A compile test is in the plan.

### 7. Force-stop runtime capability was missing

**Resolved.** No new runtime method is required. Force uses the existing
`StopLightVM`. If QEMU remains alive, force does not clear ownership or delete
the group; the member stays `removing` with a “QEMU still running” error. If
QEMU stopped but cleanup fails, force sets desired stopped/status error, clears
the LightVM owner, leaves an explicit orphan, and deletes the member/group. This
prevents a live orphan and reuses the lifecycle.

### 8. Proto delete response was contradictory

**Resolved.** Group delete always returns `DeleteLightVMGroupResponse` with
`state`; `httpResponseStatus` maps `deleted` to 204 (no body) and `deleting` to
202. The annotation is concrete; the plan explicitly forbids unresolved
`body`/additional-binding placeholders.

### 9. Controller/guard interface types were undefined

**Resolved.** A shared `internal/fleet` package defines `Guard` and `Reconciler`,
imported by both `api` and `gateway`. `api.Server` implements `Guard` and holds a
`Reconciler`; `Gateway` exposes `FleetController() fleet.Reconciler`;
`serve` calls `WithFleetGuard(apiServer)`, `SetFleetController`, and
`RunFleetReconciler(ctx)`. No duplicate named interface types and no import
cycle.

--------------------------------------------------------------------------------

## Other Review Fixes

*   **Repair version:** member `template_version_id`/`template_version` are now
    operational; repair uses the member version. Test: move to v2, delete a v1
    member’s LightVM, repair stays v1.
*   **Member power actions:** new group member start/stop endpoints update intent
    even when the LightVM row is missing. Direct `/start` on a member is rejected
    while paused/deleting/provisioning/removing; the fleet uses an internal start
    wrapper.
*   **Status scan race:** `reconcileLightVMStatuses` skips group members; the
    fleet owns member status. Tests cover API restart during a start.
*   **VPP epoch:** gated-phase failure clears ready/epoch; VNF/NAT failures do
    not. Autostart stays after the VPP phase.
*   **Group version race:** `CreateGroupMemberIntent` takes `expectedVersionID`
    and rejects a changed pin in the insert transaction.
*   **Counts/convergence:** `actual_count` = members with a LightVM;
    `provisioning_count`, `removing_count`, `converged` added.
    `wait_for_ready` requires converged and running_count == desired.
*   **Force live orphan:** force refuses to clear ownership while QEMU is alive.
*   **Sequential worker test:** manual step 15 now says interactive API writes
    continue, but other groups reconcile on a later pass, not concurrently.
*   **Terraform timeouts:** `timeout` 2h, `destroy_timeout` 6h (max 24h),
    documented as best-effort.
*   **Interface cap:** effective cap is 24 combined; the standalone 32 is
    removed.
*   **VM autostart:** gated VPP phase then non-gated NAT/VNF then autostart;
    VNF/NAT failures do not block VM/fleet autostart.
*   **Tests:** force vs in-flight start, repair failure, provisioning adopt,
    delete during pull, VPP-down cleanup, kouja owner clearing, exact budget,
    controller compile, status-scan skip, group version race, member actions, and
    Terraform import/destroy.

--------------------------------------------------------------------------------

## Accepted Limitations

1.  No capacity admission.
2.  Sequential fleet work; a slow image pull delays later groups.
3.  QEMU-level readiness, not guest health.
4.  No VM or disk-backed group members.
5.  VPP-down interface cleanup backs off; force can leave an owned-interface
    leak in inventory.

--------------------------------------------------------------------------------

## Post-Review Fixes

A targeted verification of revision 6 found two remaining blockers and several
contract gaps. They are fixed in the current document:

*   **Member backoff persistence:** `lightvm_group_members.retry_attempts` and
    `SetGroupMemberRetry` are now in the DDL/contract. Create/repair backoff is
    persistable; stopped intent is not lost.
*   **Kouja repair collision:** member stems are 51 characters; repair resources
    use a four-hex attempt suffix (`<stem>-d<position>-r<4hex>`). A new disk does
    not collide with an old `deleting`/`delete_failed` row.
*   **Active liveness:** the loop explicitly restarts an active member whose
    QEMU disappeared, and stops a live member whose intent is stopped. CUJ 2 is
    covered again.
*   **Scale-to-zero:** scale-in uses tracked excess, removes failed
    `provisioning` members with no LightVM first, then newest active. `desired=0`
    reaches convergence.
*   **Force orphan attachments:** force leaves the orphan LightVM and its
    attachments intact and skips deleting those resources; it deletes only
    unattached owned resources.
*   **Template proto surface:** all template/version request and response
    messages are defined.
*   **Interface owner guard:** `normalizeLightVMInterfaceIDs` takes the target
    member ID; Create/Update pass it explicitly.
*   **VPP reset:** `watchVPP` resets ready/epoch on ping failure or identity
    change, not only after a failed gated phase.
*   **Public disk retry:** no blanket `MarkDiskDeleting` change; fleet cleanup
    inspects `DeletionState` and skips queued/failed disks.
*   **Power generation:** `SetGroupMemberPower` bumps
    `lightvms.lifecycle_generation` when it writes the mirror.
*   **Counts/version lookup:** aggregate counts and
    `GetLightVMTemplateVersionByID` are store contracts; no N+1 reads.
*   **UI/start semantics:** ready uses `running_count == desired`; member
    start/stop actions are async intent updates.
*   **Not-found cleanup:** `StopLightVM`/`DeleteLightVM` not-found is treated as
    already gone.
*   **State diagram/CUJs:** `active → provisioning` on LightVM loss is explicit.

--------------------------------------------------------------------------------

## Second Targeted Pass

A second targeted review found two blockers and several contract gaps. They are
fixed in the current revision:

*   **Proto path/field alignment:** template-version and member-action path
    variables now match request fields (`id`, `version_id`, `member_id`); group
    Get/List requests and template request/response messages are defined.
    `response_body` is set for flat list/group HTTP bodies.
*   **Total stopped-intent adoption:** an existing stopped LightVM with stopped
    intent now marks the member `active` immediately; a live one is stopped and
    then marked active. No member remains `provisioning` with a valid stopped
    workload.
*   **Stale `starting` recovery:** `starting` is in-flight only while
    `StartCoordinator.Busy`; otherwise the status is repaired/restarted.
*   **Start result classification:** `AlreadyExists` is success; `Aborted` is
    re-read/retried without backoff; only real failures increment attempts.
*   **Backoff wiring:** repair/adopt/removal check `next_retry_at`; success clears
    member/group retry fields. Group backoff is reserved for group-level
    validation failures.
*   **Scale-in ordering:** excess is computed before recovery; failed
    provisioning members selected for removal are not repaired first.
*   **No-op status reads are free:** only state-changing actions consume the
    pass budget, so ten status reads cannot starve scale-in.
*   **Pass deadline:** `passCtx` is derived once and passed to every action; the
    budget/deadline are checked before each action.
*   **VPP rebuild deletion:** interface cleanup verifies the current name/index
    or backs off; it never deletes a stale `sw_if_index` during a rebuild.
*   **Interface attach contract:** `SetInterfaceAttachment` keeps its VM-only
    `(VirtualInterface, error)` signature; LightVM attachments use
    `lightvm_interfaces` with the target member guard.
*   **Read-only disk guard:** the group-owner check runs before the read-only
    early return.
*   **Wake/count semantics:** `WakeFleet` is a coalescing buffered non-blocking
    send; `actual_count` consistently means members with a LightVM; ready means
    `running_count == desired_count`, separate from `converged`.
*   **Tests:** stale `starting`, stopped-intent adoption, start classification,
    scale-in ordering, free status reads, VPP rebuild, proto shapes, and
    interface-attach guards are in the plan.

No production code has been changed. The design is ready for an implementation
approval check; no known blocker remains from this pass.
