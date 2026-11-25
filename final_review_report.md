# Final Targeted Review — Revision 5

*   **Target:** `docs/designs/instance-templates-and-groups.md` (1655 lines)
*   **FeatureRange:** 1–1266
*   **DetailedRange:** 1267–1655
*   **Mode:** Targeted remediation review
*   **Reviewers:** `egm-design`, `egm-implementation-readiness`

--------------------------------------------------------------------------------

## Verdict

**Not implementation-ready.** Revision 5 fixes the ownership, LVM, count-guard,
and risk-section problems, but the final review found **9 remaining blockers**.
Most come from one root cause: the fleet path invents a parallel LightVM
lifecycle instead of reusing the existing `StartCoordinator`, `runMu`,
`lifecycle_generation`, and cleanup reservation. The plan also uses `int64`
LightVM IDs where the codebase uses UUID strings, and assumes a force-stop
runtime capability that does not exist.

The design is closer, but another line-edit pass will not fix these. Revision 6
needs to align with the existing lifecycle or narrow the guarantees.

--------------------------------------------------------------------------------

## Twelve-Blocker Audit

| # | Revision-4 blocker | Status | Why |
| --- | --- | --- | --- |
| 1 | Force does not enter deletion / can create replacements | **Fail** | Force state and intent guard are specified, but an in-flight create that already inserted a member/LightVM is not canceled or revalidated after image pull. It can start after force. |
| 2 | Force cannot complete when `DeleteLightVM` fails | **Pass** | Owner clear + stopped/error orphan path is defined. Caveat: a live QEMU can remain if the forced kill fails. |
| 3 | Member power intent lost on out-of-band delete | **Fail** | Normal repair preserves intent, but a **failed repair** moves the member to `removing`, deletes its row, and the next create uses the default `running` intent. |
| 4 | Member power dual-written | **Pass** | `SetGroupMemberPower` is specified as one transaction. |
| 5 | Two tracked-count guards | **Pass** | Both use the tracked member count. |
| 6 | LVM provisioning row claimed by reclaimer | **Pass** | LVM uses create-then-insert with rollback; no LVM provisioning row. |
| 7 | “Only start/stop/console” excludes delete | **Pass** | Direct delete allowed and triggers repair. |
| 8 | Start-failure semantics contradict create path | **Partial** | Initial vs restart split is right, but crash recovery for `provisioning` + existing LightVM is missing, and `status='creating'` is not a valid status. |
| 9 | Attach ownership not enforced | **Pass at design level** | Guards are listed, but the store signatures need a target member ID; see Major. |
| 10 | No total per-pass action budget | **Fail** | Budget is reset per group, missing-to-provisioning marking is unbudgeted, and there is no pass deadline. |
| 11 | VPP epoch pass-scoped / starts ungated | **Pass with caveat** | Worker paths gate creates/starts; direct `/start` bypasses; a network-phase failure does not clear the ready flag. |
| 12 | `resourceMu` held across image pulls; wiring undefined | **Fail** | Releasing the lock without the existing start coordinator creates a delete/start race. Controller/guard interface types are also undefined. |

Seven pass or pass-with-caveat; five fail or partially fail.

--------------------------------------------------------------------------------

## Remaining Blockers

### R1. The action budget is per group, not per pass

**Scope:** §4.1 699–709; §4.4 823–854; §4.5 826–849; NFR 1180–1184; plan
1433.

`budget = fleetMaxActionsPerPass` is initialized inside `for each group`, so a
pass can perform ten actions per group, not ten total. The
“mark active members whose LightVM is missing provisioning” step is unbounded
and does not decrement the budget. Sequential groups plus 15-minute create
timeouts also mean there is no bounded pass duration.

**Fix:** one counter per `ReconcileFleet` call, passed through every helper and
decremented by every member transition, including missing-to-provisioning
marking. Add a pass deadline. Or explicitly rename to
`fleetMaxActionsPerGroup`, add a separate total cap, and change the NFR.

### R2. A failed repair loses stopped power intent

**Scope:** 54–62, 392–416, 759–770, 795; `desired_state` default.

A repair failure moves the member to `removing`; ordinary cleanup deletes the
row; the next create uses `running`. A stopped member whose repair fails returns
running with a new member row/name. This reintroduces revision 4’s B2.

**Fix:** do not delete the member row on repair failure. Keep it `provisioning`
with error/backoff until repair succeeds, or persist intent outside the row.
Test: stopped member → out-of-band delete → repair failure → retry → still
stopped.

### R3. `provisioning` + existing LightVM has no recovery; `creating` is invalid

**Scope:** 737–744, 759–761, 833–835, 392–416, 739; `store.go:142-168`,
`reconcile.go:232-238`, `lightvm_instances.go:194-215`.

If the API crashes after inserting the LightVM but before start/active, the
member stays `provisioning` and the LightVM exists. The repair step only handles
a *missing* LightVM, so the member is stuck. `status='creating'` is not one of
the valid statuses (`stopped`, `starting`, `running`, `stopping`, `paused`,
`error`, `terminated`); `reconcileLightVMStatuses` does not handle it.

**Fix:** define recovery for `provisioning` + existing LightVM (adopt if running,
restart if desired running, or delete/recreate). Use a valid status
(`starting`/`stopped`) and add crash tests after insert, after prepare, and after
QEMU start.

### R4. Fleet start bypasses the existing lifecycle serialization

**Scope:** 737–744, 759–770, 896–908; `lightvm_instances.go:181-385`,
`lightvm/start_coordinator.go:47-167`.

The internal path inserts under `resourceMu`, releases it, then calls
`PrepareLightVM`/`StartPreparedLightVM` with only an intent/epoch recheck. It
does not use `StartCoordinator.Begin`, `runMu`, `lifecycle_generation`, or
`ReserveForCleanup`. A concurrent `/stop`, `/delete`, `ForceStopLightVM`, or
cleanup can run after the recheck and before/during QEMU start. The fleet can
start a deleted row or a member intended to stop; force is not absolute.

**Fix:** route fleet create/repair starts through a shared helper that uses the
start coordinator, serializes status with `runMu`, checks `LifecycleGeneration`
after prepare, and rechecks group `state`, `force_delete`, and member `state`
immediately before start. Use `ReserveForCleanup` for fleet delete/repair.
Test `/stop` and `/delete` during a blocked image pull.

### R5. “Cleanup without VPP” is not implementable for interfaces

**Scope:** 689–690, 787–795, 901–902; `interface_writes.go:32-40, 152-186`.

Deleting a group-owned interface requires `interfaceService.DeleteInterface`,
which calls `vppReady()` and `DeleteVhostUser`. If VPP is down, normal cleanup
and group deletion cannot delete interfaces; the member stays `removing`. Force
can complete only by clearing ownership and leaving the interface in inventory.

**Fix:** define a DB-only interface delete with later VPP reconciliation/leak
handling, **or** narrow the claim to “no epoch guard” and allow cleanup to back
off until VPP returns. Test scale-in and group deletion with VPP down.

### R6. Store contracts use `int64` for LightVM IDs

**Scope:** plan 1405, 1412–1413; `store/lightvms.go:72`; migration
`0044_lightvms.sql:5`.

`store.LightVM.ID` is a UUID string and `lightvms.id` is `TEXT`.
`SetGroupMemberPower(ctx, memberID int64, lightVMID int64, ...)`,
`SetLightVMDesiredState(ctx, lightVMID int64, ...)`, and
`ClearLightVMGroupOwner(ctx, lightVMID int64)` cannot compile. An existing
`SetLightVMDesiredState(ctx, id string, state string) (LightVM, error)` already
exists; the plan must preserve or extend that signature.

**Fix:** use `string` LightVM IDs throughout. Keep the existing store method
name/signature or add a separately named helper. Add a compile test.

### R7. Force-stop runtime capability is missing

**Scope:** 809–817; plan 1441; `internal/lightvm` not in the file list.

The design says force cleanup “kills the attempt PID and removes run-dir/
container artifacts,” but `LightVMRuntime` exposes only `StopQEMULightVM` and
`CleanupStoppedLightVM`; cleanup refuses while QEMU runs. The gateway does not
know the attempt PID, run-dir, or container name.

**Fix:** add a concrete runtime method (for example `ForceStopLightVM`) with
tests and the `internal/lightvm` file change, or remove the force-stop guarantee
and keep the member/group in `deleting` with an explicit error.

### R8. Proto delete response contract is contradictory

**Scope:** 478–480, plan 1287–1293.

One gRPC method has one response type. The plan says deletes return
`google.protobuf.Empty` for 204 and `DeleteLightVMGroupResponse` for 202. The
annotation leaves `body: "*"?` and “additional_bindings if needed” unresolved.

**Fix:** one `DeleteLightVMGroupResponse` with a `state` field; map
`deleted` → 204 and `deleting` → 202 in `httpResponseStatus`. Specify the exact
`google.api.http` delete annotation and `force` query field.

### R9. Controller/guard interface types are undefined

**Scope:** 676–684; plan 1446–1463.

`FleetGuard` is described in the gateway, while `SetFleetController` and
`RunFleetReconciler` are API-side. The plan does not state where the shared
interface type lives or that `api` imports `gateway` for the exact type. Two
identically shaped named interfaces in different packages are not assignable.

**Fix:** define the exact `FleetGuard` and `FleetController` types, their
package ownership, and the import direction. Put the guard interface in a small
shared package or make the worker accept function values
(`func() uint64`, `func(uint64) bool`) to avoid a cycle. Add a compile test.

--------------------------------------------------------------------------------

## Remaining Majors

*   **Repair uses the wrong version.** §4.3 says “from the pinned template
    version,” but CUJ 7 says existing members keep their version. Repair must
    use `member.template_version_id`; test move to v2, delete a v1 member’s
    LightVM, repair stays v1.
*   **Direct `/start` ignores pause/deleting.** A paused or deleting group can
    be started through the public member start action, bypassing the worker.
    Reject start for paused/deleting groups and `provisioning`/`removing`
    members, or define operator override semantics.
*   **Power actions cannot address a missing LightVM.** `/start` and `/stop` are
    keyed by LightVM UUID. If the row is missing, the request returns 404 and
    cannot update the member row. Add a member-level action or remove the
    missing-LightVM clause.
*   **Status scan and fleet start race.** The plan starts both goroutines with
    no barrier; `reconcileLightVMStatuses` can overwrite a fresh start to
    `stopped`. Add a `statusReconciled` barrier or skip group members in the scan.
*   **VPP ready flag is not cleared on phase failure.** On a network/interface/
    ACL/route-table phase failure after a previous success, the ready flag can
    remain true. Clear it and invalidate the epoch on any gated-phase failure.
*   **Group version races the member insert.** `CreateGroupMemberIntent` checks
    count/pause/force but not the current `template_version_id`; a concurrent
    group move can create a member under the old version. Include the expected
    version in the insert guard.
*   **Count convergence is ambiguous.** `actual_count` excludes `removing`
    members, so a group can report `drift=0` while removals still run. Define
    `actual_count` as all members with a LightVM, or add `provisioning_count`/
    `removing_count` and a convergence predicate. Specify `wait_for_ready`.
*   **Sequential worker vs manual E2E #15.** A blocked image pull blocks the
    single fleet worker; “another group can reconcile” is false. Change the test
    to “interactive API writes continue” or add per-group workers.
*   **Force can leave a live orphan QEMU.** If `ForceStopLightVM` fails, force
    still clears ownership and deletes the group/member. Verify QEMU is not
    running before clearing, or add an orphan-process reaper.
*   **Terraform 2h destroy claim is inaccurate.** 1024 members at ten actions
    per pass is at least 103 passes, before per-member cleanup. State best-effort
    vs worst case or raise the default.
*   **Test gaps.** Missing force vs in-flight create, repair after version move,
    missing-LightVM power actions, delete during image pull, status-scan race,
    VPP-down cleanup, kouja owner clearing, exact budget accounting, and a
    controller-wiring compile test.
*   **Interface cap 32 vs 24.** The spec still says `0..32`; the effective cap is
    24. Remove the 32 or state it as a secondary cap.

--------------------------------------------------------------------------------

## Nits

*   `DeleteLightVMGroupResponse` says `state="deleted"` for 204 while the plan
    says 204 returns `Empty`.
*   Proto instructions still contain “`body: "*"?`” and “if needed.” Replace with
    an exact annotation.
*   Initial create failure moves to `removing`, which deletes the member row;
    this contradicts the absolute “member row is stable” statement. State the
    rollback exception.
*   Member `template_version_id` is called informational while repair semantics
    depend on it.
*   VM autostart placement after splitting `reconcileInfrastructure` is not
    specified; add a regression test that VNF/NAT failures do not stop VM
    autostart.

--------------------------------------------------------------------------------

## Recommendation

Revision 6 should do one of two things:

1. **Reuse the existing lifecycle.** Fleet create/repair/delete calls a shared
    helper built on `CreateLightVM`/`StartLightVM`/`DeleteLightVM`,
    `StartCoordinator`, `runMu`, and `lifecycle_generation`. Use string IDs, add
    a real force-stop runtime method, make the action budget per pass, and
    resolve the proto/controller contracts. This keeps the full scope but is a
    real code refactor.
2. **Narrow v1.** Use the existing public create/start/delete paths; no
    release of `resourceMu` during image pull; no repair-in-place or force-stop
    guarantee. A failed member stays `active`/`error`; a failed group delete
    stays `deleting` with an error. This is smaller and avoids reimplementing
    the lifecycle, but loses the no-interactive-blocking and force guarantees.

Do not start implementation from revision 5. No production code has been
changed.
