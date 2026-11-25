# Stage 5: Initial Zoom Out Review — Revision 4

*   **Target:** `docs/designs/instance-templates-and-groups.md` (1528 lines, revision 4)
*   **Range:** Full document, lines 1–1528
*   **Mode:** Consolidated full-flow review
*   **Reviewer:** `egm-zoom-out`

--------------------------------------------------------------------------------

## Findings

### 1. Blocker — The member row is deleted before its stored power intent can reach a replacement

**Affected:** Overview 59–62; Decisions 195–198; CUJ 3/4 248–265; §4.4 step 4 818; §4.5 840–857; test plan 1450.

The worker marks an out-of-band-deleted member `removing`, deletes its resources, then deletes the member row. Only later does `tracked < desired_count` allow a replacement. The replacement inserts with the DDL default `desired_state='running'`; the old member’s stored intent is gone. A stopped member deleted out-of-band comes back running, violating the reason for storing `desired_state` on the member row. The test plan has no stopped-member-plus-out-of-band-delete case.

Fix: keep the member row until the replacement is created with the old intent, persist a replacement intent on the group/pending state, or create the new member row carrying `desired_state` in the same cleanup transaction. Add the missing test.

### 2. Blocker — The new LVM provisioning row is immediately claimable by the disk reclaimer

**Affected:** §4.2 771–774; Reliability 1198–1199; plan 1358; `internal/store/disk_deletion.go:160–234`; `internal/storage/reclaimer.go:49–70`; existing kouja pattern `disk_kouja.go:44–57`.

`ClaimDueDiskDeletions` includes `DiskDeletionProvisioning` rows whose `deletion_next_attempt_at <= now`; `FinalizeProvisioning` requires `deletion_claimed_at = 0`. A newly inserted LVM row with the default zero timestamp is claimed on the next reclaimer tick, which can call `lvremove` while the worker is creating the volume. Kouja avoids this with a 20-minute `DeletionNextAttemptAt`; the design says to reuse the kouja remove path but omits the create guard.

Fix: reuse the kouja provisioning pattern (state plus future next-attempt/claim) for LVM, or create the row inside the same claim/lease mechanism; test a slow LVM create against a reclaimer tick. Do not change global `MarkDiskDeleting` retry semantics for group cleanup; have the fleet skip already queued/deleted disks instead.

### 3. Major — The per-pass cap claim is false

**Affected:** §4.5 840–857; Performance 1208–1209.

Only scale-in (849) and creates (856) are capped. Cleanup (841) and restarts (852–853) are unbounded, and each restart can consume the 15-minute timeout. A 1024-member group with many dead members can monopolize the single worker and block every later group. The prior finding remains.

Fix: cap restarts and cleanup as well, or change the NFR claim and add a real pass deadline. Add a >10 dead members test.

### 4. Major — The VPP epoch gate is pass-scoped, not operation-scoped, and starts are not gated

**Affected:** §4.1 730–745; §4.3 794–798; §4.8 891–899; `internal/api/reconcile.go:19–56, 389–500`.

`canProvision` is computed at pass start. A VPP restart during a long image pull is invisible to the pass, and `CreateInterface` checks only readiness, not the generation. Recovery starts a found running-desired LightVM without consulting `canProvision`, while global autostart runs only after VPP resources are reconciled so vhost sockets exist. The barrier is advisory. The controller (gateway) and the loop/epoch (api) are in different packages.

Fix: pass the epoch token into create/interface/start operations and recheck before each external write; gate starts of members with interfaces; keep the loop and controller in one component. Track success of the specific network/interface/ACL/route-table steps, not the joined error.

### 5. Major — Group-owned disks and interfaces can be attached to unmanaged workloads

**Affected:** §3.4 695; plan 1358–1360; `store.go:1670–1685, 2429–2443`; `lightvms.go:695–719`.

`checkDiskAvailableForAttach` checks only `workload_disks`, not `disks.group_member_id`. `normalizeLightVMInterfaceIDs`/`checkLightVMInterfaceUse` check only `lightvm_interfaces`, not `virtual_interfaces.group_member_id`; `SetInterfaceAttachment` can attach a group-owned interface to a VM. An unmanaged workload can hold a group resource. Cleanup refuses to delete it; the member sticks in `removing`. The plan says “reject foreign attachment” but the invariants/validation table do not define it.

Fix: reject attaching any resource with non-null `group_member_id` to a different workload at the store boundary; add VM and LightVM attach tests.

### 6. Major — The 32-interface validation contradicts the 24 combined device cap

**Affected:** §3.4 688; §9 1238; `store.go:110–111`; `lightvms.go:129–152, 426–428`.

The design allows up to 32 interfaces in a template, but `validateLightVMDeviceBudget` rejects more than 24 combined disks/interfaces/shares when the member is created. A template with 25–32 interfaces validates but can never produce a member; every create fails and backs off. The 32 figure is the console/proto limit, not the LightVM device limit.

Fix: validate templates against `store.MaxLightVMDevices` (24 combined) and remove the standalone 32 interface limit, or change the store limit deliberately. Add a 25-interface test.

### 7. Major — Attachment-mismatch recovery has no usable operator action, and missing resources are not handled

**Affected:** §4.3 803–806; §3.4 695; §4.4 810–817.

The design says “the operator resolves it by deleting the extra attachment,” but all direct `UpdateLightVM` calls are rejected for group members, and `DeleteDisk`/`DeleteInterface` refuse attached resources. The practical actions are deleting the workload (if allowed) or forcing the group. The text also says “do not delete a workload the member does not own” for a workload that carries `group_member_id`, which is contradictory. Missing owned resources are not detected.

Fix: define a detach-only repair path or make “delete the member workload and let the fleet replace it” the repair. Detect missing and extra attachments; test both.

### 8. Major — Force is not a guaranteed escape hatch while `lightvms.group_member_id` is RESTRICT

**Affected:** §4.4 826–834; §4.7 882–889; DDL 403–404; `lightvm_instances.go:181–235`.

Force promises to stop/delete every owned LightVM. `DeleteLightVM` returns an error if QEMU cannot be stopped or cleaned up; the member row cannot then be deleted because the LightVM FK is RESTRICT; the group remains. There is no hard-stop escalation or ownership-detach fallback. The escape fails exactly when needed.

Fix: define a force stop that kills the attempt PID, or allow force to clear `lightvms.group_member_id` and leave the workload for the operator while deleting the group. State and test a stuck-QEMU case.

### 9. Major — Template validation/creation and ACL assignment duplicate existing LightVM/interface code

**Affected:** §3.3 576–594; §3.4 681–696; §4.2 775–780; plan 1351–1353; existing `lightVMFromRequest` 636–770, `validateLightVMReferences` 868–882, `validateCgroupClassFor` 367–394, `SetInterfaceACLs` 300–338, `resolveInterfaceACLs` 340–369.

`LightVMTemplateSpec` repeats the LightVM create fields and validation, so `fleet_validate.go`/`fleet_provision.go` will diverge from `lightVMFromRequest` and `CreateLightVM`. The ACL helper repeats `EnsureVPPACL`/`SetACLVPPIndex`/`aclIndexReference`/rollback, differing only in ID vs name resolution. This creates a second source of truth.

Fix: convert a template spec into the existing `store.LightVM`/create request and reuse validators; refactor `SetInterfaceACLs` into an ID-based core and reuse it. Keep one validation path and one ACL apply/rollback path.

### 10. Major — The design retains an unrelated kouja VM behavior change

**Affected:** Compatibility 1218–1220; `vm_create.go:246–279`.

“kouja VMs no longer require the default thin pool” is a VM create change, but VM groups are deferred and the plan does not include `vm_create.go`. The existing code still requires the thin pool for kouja-only VMs. The line is false or unplanned scope creep.

Fix: remove the line and the “kouja thin-pool exception” or add a separate VM change with tests.

### 11. Major — The detailed implementation plan and review artifacts remain in the design

**Affected:** Revision note 8–15; Detailed Plan 1271–1522.

The design is 1528 lines and includes file/test inventories, store contracts, helper signatures, a `/tmp` path, and a root review report. Other project design docs are architecture-level. This violates anti-weeds and makes a fresh reader depend on non-durable review history.

Fix: move the implementation plan to `docs/plans/` or an appendix, remove `/tmp` and review-report references, keep the design component/interface/data-flow level.

### 12. Major — Terraform `current_version_id` has undefined import/refresh semantics

**Affected:** §8 989–1010.

`current_version_id` changes when the template spec changes; a group referencing it moves on the next apply, not the “explicit pin” model. Import by template ID has no rule for selecting a version; `Read` cannot recover if the version was deleted. There is no version data source/resource.

Fix: define import/refresh explicitly, or simplify to a version data source/resource and make the group pin a literal/immutable version. Document that template edits do not roll existing members.

### 13. Major — “Only start/stop/console” contradicts CUJ 4 and recovery

**Affected:** Goals 155–157; §3.4 695; CUJ 4 258–265; §4.6 879–880.

CUJ 4 deletes a member through `DELETE /api/lightvm/instances/{id}`, and the operator is told to delete its workload to force replacement. If delete is rejected because “only start/stop/console” is allowed, the self-healing path cannot be triggered.

Fix: state “definition updates rejected; start/stop/console/delete allowed” and test the delete-and-replace path.

## Nits

*   **Template `config_files` is dead surface.** Lines 584, 689, 996: image-backed group templates reject config files; remove it from proto, validation, Terraform, and UI until disk-backed groups exist.
*   **`member_name` uniqueness is case-sensitive.** Line 386 uses `UNIQUE`, while line 683 promises case-insensitive unique names. Use `COLLATE NOCASE UNIQUE` or align.
*   **Redundant store/API surface.** `ClearResourceOwners` duplicates `ON DELETE SET NULL`; `DeleteLightVMGroup`/`DeleteGroupIfEmpty` overlap; member `template_version_id`/`template_version` duplicate each other while the group pin keeps the version row alive. Remove or merge.
*   **`running_count`/`run_state` scale poorly.** Lines 648, 666, 1210, 1008: group detail probes every member; use stored status for counts and probe live `run_state` only for the visible page or on demand.
*   **`paused` has no CUJ.** Lines 196, 863–880, 971, 990: add a pause journey or remove the state axis.
*   **Labels are not extended.** Templates/groups are new inventory; add `lightvm_template`/`lightvm_group` label types or state the non-goal.
*   **Test plan omits claimed cases.** Lines 1444–1461 omit group delete during provisioning, missing-plus-live scale-in, >10 dead members, stopped-member replacement, and the LVM reclaimer race. Add them.

## Verified Claims

*   Adding `group_member_id` to `lightvms` fits the existing `CreateLightVM` transaction; disk/interface owner columns fit `CreateDisk`/`CreateInterface`, subject to the attach guard and LVM race.
*   `autostartLightVMs` can skip `GroupMemberID != nil`; `reconcileLightVMStatuses` is unaffected.
*   A fleet worker can run outside `watchVPP`; readiness is available. The gap is operation-level epoch enforcement, not placement feasibility.
*   The kouja delete reuse is accurate: `DeleteDisk` queues, the reclaimer deletes, `SET NULL` releases the member. The inaccurate reuse is the new LVM create path.
*   The LightVM-only scope removes the VM shell, VM power race, VM UI, and duplicate VM template surface. The remaining VM reference is the stale kouja thin-pool line.
