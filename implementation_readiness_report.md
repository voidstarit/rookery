# Stage 9: Implementation Readiness — Revision 4

*   **Target:** `docs/designs/instance-templates-and-groups.md` (1528 lines)
*   **DetailedRange:** 1271–1528
*   **Mode:** Consolidated full-flow review
*   **Reviewer:** `egm-implementation-readiness`

--------------------------------------------------------------------------------

## Verdict

**Not implementation-ready.** The detailed plan is a credible file inventory, but
it does not resolve the feature-range blockers. Ten plan-level blockers remain.
An implementer would have to make lifecycle, ownership, and wiring decisions that
the document leaves open, and several listed phases cannot be executed as
written.

--------------------------------------------------------------------------------

## Blockers

### 1. Force never enters `deleting` and cannot complete with a failing LightVM delete

**Affected:** §4.4 826–834; §4.7 882–889; store contract
`SetLightVMGroupDeleting` 1318; `lightvms` FK 403–404, 418–419.

`force=true` sets only `force_delete=1`; the plan never says to set
`state='deleting'`. The loop only performs mass removal inside
`if group.state == deleting`, so force on an active group marks members
`removing` and then creates replacements. If `DeleteLightVM` fails,
`lightvms.group_member_id` is `RESTRICT`, so the member cannot be deleted and no
`ClearLightVM...` method exists. **Fix:** `SetLightVMGroupDeleting` atomically
sets `state='deleting'` and `force_delete=1` for active/deleting groups; add an
explicit force branch; define the failure policy for an undeletable LightVM
(clear owner and leave an explicit orphan, or fail the group delete); test a
failing `DeleteLightVM`.

### 2. Out-of-band delete loses the stored power intent

**Affected:** CUJ 4 258–265; §4.3 794–800; §4.5 846–859;
`CreateGroupMemberIntent` 1323.

The member row is the only power-intent store, and cleanup deletes it before a
replacement exists. The replacement goes through the generic create path with no
stated intent capture. A stopped member deleted out of band comes back running.
**Fix:** define how intent is captured/retained and passed to the replacement,
including a stopped-create path; test deletion of a stopped member. The preferred
fix is to repair the same member in place rather than delete the member row.

### 3. Start-failure semantics contradict the existing create path

**Affected:** §2.4 463–465; §4.2 775–787; `lightvm_instances.go:103-136`.

§2.4 says a start failure after the workload exists stays `active` with
error/backoff; §4.2 says a resource/workload failure after insert moves to
`removing` and cleans up. The existing public `CreateLightVM` starts immediately
and rolls back on failure. The plan says only “owner-aware internal create.”
**Fix:** define a separate internal create helper; state whether a start failure
keeps the record (`active` + backoff) or rolls it back (`removing` + cleanup);
test both create-start and restart-start failures.

### 4. Two different tracked-count guards

**Affected:** §3.2 544–545; §4.2 768–770; §4.5 854–856;
`CreateGroupMemberIntent` 1323, 1338–1339.

The insert guard is `desired_count > active_count`; the loop creates only when
`tracked = active + provisioning + removing < desired_count`. During scale-in or
cleanup, `active_count` is lower than `tracked`, so the store guard admits extra
members. **Fix:** one guard, preferably
`COUNT(*) WHERE state IN ('active','provisioning','removing') < desired_count`,
computed in the insert transaction and used by the loop; test a mixed-state
group at `desired_count`.

### 5. Attach ownership is only partially enforced

**Affected:** §3.4 695; §4.4 808–819; Phase 3 1357–1360; manual step 11
1502–1503.

The plan mentions “reject foreign attachment” only in `interface_writes.go`;
there is no disk rule, and the actual attach paths are in the store
(`CreateLightVM`/`UpdateLightVM`). An unmanaged LightVM can still attach a
group-owned disk/interface, blocking cleanup. Manual step 11 assumes a foreign
attach succeeds, contradicting the rule. **Fix:** reject attaching any resource
with non-null `group_member_id` unless the target member matches; apply to both
create and update; add disk/interface tests; rewrite step 11 to verify rejection.

### 6. No total per-pass action budget

**Affected:** §4.1 741; §4.5 839–859; NFR 1208–1209.

`fleetMaxMembersPerPass = 10` caps only creates and scale-in. Recovery, removal
advancement, force marking, restarts, and cleanup are unbounded; a deleting or
force group can process 1024 members in one pass. **Fix:** one per-pass counter
consumed by every member action; test a mixed pass.

### 7. VPP epoch is checked once per pass; starts are not gated

**Affected:** §4.1 735–745; §4.5 852–856; §4.8 891–897.

`canProvision` is a boolean computed at pass start. A VPP restart during a long
pass is invisible; creates/interface writes can continue against stale state.
Restarts are never gated even though they start QEMU with VPP-backed interfaces.
**Fix:** pass the generation/epoch into the controller; recheck before each
create, interface write, and member start; abort/skip on change; test a mid-pass
reset.

### 8. `resourceMu` is still held across image pulls

**Affected:** §4.1 743–745; §9 1033–1035; `lightvm_instances.go:114-136`.

The plan documents that a slow image pull blocks provisioning but does not change
the behavior. `CreateLightVM` holds the shared `resourceMu` through
`PrepareLightVM`/image pull. One pull blocks all groups and interactive writes.
**Fix:** internal create helper releases `resourceMu` before prepare/start,
reacquires for the DB/attachment phase, revalidates member/group state; test a
blocked pull.

### 9. LVM provisioning-row/reclaimer ordering is unsafe

**Affected:** §4.2 772–774; Phase 3 1358–1359; store change 1299;
`disk_deletion.go:160-234`.

The plan inserts the LVM row in `provisioning` before `lvcreate` but does not set
`DeletionNextAttemptAt` or a claim. `ClaimDueDiskDeletions` claims any
provisioning row with a zero timestamp, so the reclaimer can `lvremove` while the
volume is being created. The `FinalizeProvisioning` arguments and failure cleanup
are unspecified. **Fix:** either keep LVM create-then-insert with rollback, or
insert with a future next-attempt/claim and finalize after `lvcreate`; test the
race.

### 10. Gateway/API/serve wiring and helper signatures are undefined

**Affected:** Phase 3 1349–1365; §4.1 719–733; `serve.go:187,274,334`;
`gateway.go:322-388`.

`gateway.New` is called after `api.New`; the plan says `serve.go` will “configure
gateway reconciler” but defines no constructor, getter, option, or
`api.Server` setter. `gateway.New` has no defined way to expose the controller;
`lightvm_groups.go` has no controller field/constructor. The owner-aware
disk/interface/LightVM helpers have no names/signatures; the VPP-ready flag has
no update path. **Fix:** define the controller constructor/dependencies, the
`api.Server` configuration method and call order, and exact internal helper
signatures (owner, desired state, ACLs, rollback).

--------------------------------------------------------------------------------

## Majors

*   **`running_count` performs unbounded runtime scans.** §2.3 436–437; §3.3
    648; NFR 1210; Terraform wait 1008–1009. `GetLightVMGroup` consults every
    member runtime, including `/proc` fallback, on every poll. Use reconciled
    `lightvms.status` or a batched/capped runtime check; benchmark a large group.
*   **Terraform import/destroy semantics are underspecified.** §8 989–990,
    1008–1009; Phase 5 1412–1413. Template import by ID cannot know
    `current_version_id`; group import is absent. Force destroy says it calls
    `?force=true` but does not say it polls until 404 under `destroy_timeout`; a
    202 otherwise makes Terraform succeed while members remain. Define template
    import (latest/v1 or `template/version`), add group import, and poll
    normal/force destroy.
*   **Dead `config_files` remains.** Spec 584; validation 689; Terraform 996,
    1007. Image-backed templates always reject it. Remove from the template spec
    and schema, or explicitly reserve/exclude it.
*   **Direct delete is still excluded from member actions.** Validation 695 says
    only start/stop/console; CUJ 4 and recovery require delete. State that
    definition updates are rejected while direct delete remains allowed and
    triggers replacement; test it.
*   **Unrelated kouja VM thin-pool claim.** Compatibility 1219–1220; no VM file
    changes in the plan. Remove the line or add explicit VM implementation/tests.
*   **Test plan omits the blocker cases and existing disk tests.** Phase 7
    1436–1461; Phase 2 1299; Phase 3 1358–1359. Missing: force with failing
    LightVM delete, stopped-member repair, start/stop intent sync, attach
    ownership, total action budget, mid-pass epoch reset, LVM reclaimer race,
    runtime-count cost, group import. `disk_writes_test.go`,
    `disk_kouja_test.go`, and `disk_deletion_test.go` are not updated.
*   **Proto phase omits HTTP annotations and delete-response mapping.** Phase 1
    1276–1288; API surface 486–503. Specify `google.api.http` annotations, body/
    query mapping (`?force=true`), and delete response types; otherwise the
    gateway routes do not exist.

--------------------------------------------------------------------------------

## Nits

*   **24 vs 32 devices.** Validation 687–688, Limits 1237–1238. A 25–32
    interface template passes validation but always fails provisioning. Enforce
    `min(32, 24 - disks - shares)` or remove 32.
*   **ACL helper receiver.** ACL contract 1369–1376; `acl_writes.go:371`.
    `interfaceACLRefs` is an `interfaceService` method; the fleet helper has no
    receiver/dependency. Make it standalone or define the dependency.
*   **UI file list mismatch.** §7 960–961 vs Phase 4 1385–1400. `app.ts` is in
    §7 but not in Phase 4. Align.

--------------------------------------------------------------------------------

## What the Plan Gets Right

*   Four additive tables and three nullable owner columns; no destructive
    migration.
*   LightVM-only service/proto surface; no VM template or VM group files.
*   Owner lookups by `group_member_id`; no name lookup and no member
    `workload_id` duplicate.
*   `ON DELETE SET NULL` for owned disks/interfaces and `RESTRICT` for the
    LightVM are the right normal-cleanup FKs.
*   Two Terraform resources with inline spec and explicit version pin match the
    provider pattern.
*   The UI is scoped to the LightVM tab; it does not touch VM inventory.

The plan needs the member-repair model and the missing lifecycle/wiring
decisions before an implementer can start.
