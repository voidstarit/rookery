# Consolidated Review Report — Revision 4

*   **Target:** `docs/designs/instance-templates-and-groups.md` (1528 lines)
*   **Revision:** 4 — LightVM-only ownership revision
*   **Review mode:** Consolidated full-flow review
*   **Stages:** 5 (Zoom Out), 6 (Comprehension), 7 (Design/Structural/Style
    Critics), 8 (Intermediate Synthesis), 9 (Implementation Readiness)
*   **Date:** 2026-11-24

--------------------------------------------------------------------------------

## Verdict

**Not implementation-ready.** The LightVM-only, image-backed scope is the right
reduction: it removes the VM boot-content problem, the VM power race, the VM UI,
and the duplicated VM template surface. The ownership columns and the
`RESTRICT`/`SET NULL` split are also sound.

However, revision 4 still has **12 blockers**. They cluster into five root
causes:

1.  **Force is not a working lifecycle.** It does not enter `deleting`, can
    create replacements while trying to delete, and cannot finish when a
    LightVM delete fails.
2.  **Member power intent is not authoritative or transferable.** A stopped
    member deleted out of band returns running; `/start` and `/stop` dual-write
    the member and LightVM rows without an ordered protocol.
3.  **The worker budget and VPP barrier are partial.** Only creates and
    scale-in are capped, restarts are ungated, and the epoch is checked once per
    pass.
4.  **Ownership is recorded but not enforced at attach.** A group-owned disk or
    interface can be attached to an unrelated workload.
5.  **The LVM “provisioning row” is claimable by the generic reclaimer**, which
    can remove a volume while it is being created.

The detailed plan also leaves the gateway/API/serve wiring undefined, so an
implementer cannot start Phase 3 as written.

Do not implement revision 4. Apply the member-repair model and the other
structural fixes in revision 5, then run one final review.

--------------------------------------------------------------------------------

## Scope Reviewed

In scope: image-backed LightVM templates, versions, groups, members, owner
columns, worker, UI, Terraform, docs.

Explicitly deferred: VM templates/groups, disk-backed LightVM groups, guest
health, autoscaling, capacity admission, stable ordinals, writable shares,
disk-template cloning, hot reconfiguration, deletion guards, version-delete API.

Accepted operating limitations: no capacity admission, sequential fleet work,
QEMU-level readiness, no VM/disk-backed group members.

--------------------------------------------------------------------------------

## Blockers

### B1. Force does not enter deletion and cannot complete with a failing LightVM delete

**Where:** §4.4 826–834; §4.5 838–859; §4.7 882–889; Decisions 205–207;
Invariants 435–438; FK 403–404; Stage 9 #1.

`force=true` sets `force_delete=1` but not `state='deleting'`. The loop only
tears down inside `if group.state == deleting`; the conditional insert checks
only `state='active'`. Force on a low-count active group marks members
`removing`, then creates replacements. If `DeleteLightVM` fails,
`lightvms.group_member_id` is `RESTRICT`; the member and group cannot be
deleted, and no owner-clearing method exists.

**Required fix:** `SetLightVMGroupDeleting` atomically sets `state='deleting'`
and `force_delete=1` for active/deleting groups. Add a force branch before
recovery/creates. Exclude `force_delete` from intent/creates. Force uses a
separate cleanup context. If QEMU is confirmed stopped but `DeleteLightVM`
fails, clear `lightvms.group_member_id`, leave the LightVM visible as an orphan,
and delete the member/group. Force on an empty group returns `204`; non-empty
returns `202`. Test a failing `DeleteLightVM` and a stuck QEMU.

### B2. Member power intent is not authoritative or preserved

**Where:** Overview 59–62; Goals 138–140; CUJ 3/4 248–265; §4.2 783–784;
§4.3 794–800; §4.5 846–859; §4.6 865–867; Stage 5 #1; Stage 7 D3/D4/Y4/Y16;
Stage 9 #2.

The member row stores `desired_state`, but cleanup deletes the row before a
replacement exists. The replacement uses the schema default `running`, so a
stopped member deleted out of band comes back running. Separately, `/start` and
`/stop` mutate `lightvms.desired_state` and the member row non-atomically; a
crash between writes can restart a stopped member or fail to restart a started
one. Recovery can overwrite a concurrent `/start`.

**Required fix:** Make the member row the single authoritative intent. Repair
the same member in place when its LightVM disappears: mark it `provisioning`,
delete/recreate owned resources, create a new LightVM with the same
`group_member_id`, keep the member `desired_state`, and mark it `active`. This
preserves intent and keeps the tracked count stable; “fresh resources” still
holds. Define `/start`/`/stop` to write member intent first (transactionally)
and reconcile the LightVM from it. Test a stopped member deleted out of band,
a crash between writes, and `/stop` racing a fleet start.

### B3. The tracked-count guard is defined two ways

**Where:** §3.2 544–545; §4.2 768–770; §4.5 847–856; Stage 7 Y1; Stage 9 #4.

The insert guard uses `desired_count > active_count`; the loop uses
`tracked = active + provisioning + removing < desired_count`. During scale-in or
cleanup, `active_count` is smaller, so the store guard admits extra members.

**Required fix:** one expression,
`COUNT(*) WHERE state IN ('active','provisioning','removing') < desired_count`,
evaluated in the insert transaction and used by the loop. Test a mixed-state
group at `desired_count`.

### B4. The LVM provisioning row is claimable by the reclaimer

**Where:** §4.2 771–774; Phase 3 1358–1359; `disk_deletion.go:160-234`;
`storage/reclaimer.go:80-93`; `registry.go:24-26`; Stage 5 #2; Stage 7 D10;
Stage 9 #9.

The plan inserts an LVM disk row in `provisioning` before `lvcreate`. The
generic `ClaimDueDiskDeletions` claims any provisioning row with a zero
next-attempt time; the default reclaimer registry only has the kouja driver, so
an lvm-thin row becomes `delete_failed`, and `FinalizeProvisioning` cannot
complete. This can `lvremove` a volume still being created.

**Required fix:** keep the existing LVM create-then-insert with rollback; do not
insert a provisioning row before `lvcreate` unless an lvm-thin reclaimer driver
and a future claim time are added. Specify finalize/failure cleanup. Test the
race.

### B5. The allowed member action set contradicts delete and recovery

**Where:** Goals 155–157; validation 695; CUJ 4 258–265; §4.6 879; Stage 7
D5/Y2/Y18; Stage 9 #14.

“Only start/stop/console” excludes `DELETE /api/lightvm/instances/{id}`, which
CUJ 4 and recovery require. The design also tells the operator to delete the
workload to force replacement.

**Required fix:** state “reject definition updates; allow start, stop, console,
and delete.” Direct delete triggers member repair/replacement, not a slot change.
Document the data-loss/replacement semantics in the UI. Test delete-and-repair.

### B6. Start-failure semantics contradict the existing create path

**Where:** §2.4 463–465; §4.2 775–787; `lightvm_instances.go:103-136`;
Style Y11; Stage 9 #3.

§2.4 says a start failure after the workload exists stays `active` with
error/backoff; §4.2 says a post-insert resource/workload failure moves to
`removing` and cleans up. The public `CreateLightVM` starts immediately and rolls
back on failure. The plan’s “owner-aware internal create” does not define which
behavior the fleet uses.

**Required fix:** define an internal create helper; initial create/start failure
→ `removing` + cleanup; restart failure of an active member → remain `active`
with error/backoff. Test both.

### B7. Ownership is not enforced on the attach paths

**Where:** §3.4 695; §4.4 808–819; Phase 3 1357–1360; `store.go:1670-1685,
2429-2443, 2487-2514`; `lightvms.go:695-719`; Stage 7 D6; Stage 9 #5.

The design adds `group_member_id` to disks/interfaces but only plans a check in
`interface_writes.go`. The actual attach paths are in the store. A group-owned
disk/interface can be attached to an unrelated VM or LightVM; cleanup then
refuses to delete it, and force clears ownership while the resource is still in
use.

**Required fix:** reject attaching any resource with non-null `group_member_id`
unless the target workload’s member ID matches. Apply to `CreateLightVM`,
`UpdateLightVM`, VM interface/disk attach, and `SetInterfaceAttachment`. Test
disk and interface foreign attachment. Rewrite manual step 11 to verify
rejection.

### B8. There is no total per-pass action budget

**Where:** §4.1 741; §4.4 820–824; §4.5 839–859; NFR 1207–1209; Stage 7 D7/Y14;
Stage 9 #6.

`fleetMaxMembersPerPass = 10` caps only creates and scale-in. Recovery, removal
advancement, force marking, restarts, and cleanup are unbounded; a 1024-member
deleting/force group can process all members in one pass, and each restart can
consume the 15-minute timeout.

**Required fix:** one per-pass action counter consumed by every member action,
processed cleanup → scale-in → restarts → creates. Test a mixed pass with more
than ten actions.

### B9. The VPP epoch is pass-scoped; starts are ungated

**Where:** §4.1 735–745; §4.5 852–856; §4.8 891–899; `reconcile.go:58-99`;
Stage 5 #4; Stage 7 D9; Stage 9 #7.

`canProvision` is computed once per pass. A VPP restart during a long image pull
is invisible; creates/interface writes can continue against stale state.
Restarts are never gated, even though they start QEMU with VPP-backed
interfaces. `reconcileInfrastructure` aggregates all phases into one error; there
is no per-phase VPP-network-ready result. `watchVPP` sets `pendingReset` on ping
failure, not identity change.

**Required fix:** add a dedicated `vppNetworkReady` result after
network/vhost/ACL/route-table reconciliation; reset the generation on the same
`pendingReset` path; pass the epoch token into create/interface/start operations
and recheck before each external write; gate starts. Test a mid-pass reset.

### B10. `resourceMu` is held across image pulls

**Where:** §4.1 743–745; §9 1033–1035; `lightvm_instances.go:114-136`;
Stage 7 D11; Stage 9 #8.

`CreateLightVM` holds the shared `resourceMu` through `PrepareLightVM`/image
pull, which can take minutes. The fleet multiplies this by up to ten creates per
pass; interactive VM/disk/interface/network writes block.

**Required fix:** an internal create helper releases `resourceMu` before
prepare/start, reacquires for the DB/attachment phase, and revalidates member/
group state. Test a blocked pull with another member able to reconcile.

### B11. Gateway/API/serve wiring and helper signatures are undefined

**Where:** Phase 3 1349–1365; §4.1 719–733; `serve.go:187,274,334`;
`gateway.go:322-388`; Stage 9 #10.

`gateway.New` is called after `api.New`. The plan says `serve.go` will “configure
gateway reconciler” but defines no constructor, getter, option, or `api.Server`
setter. `gateway.New` has no defined way to expose the controller;
`lightvm_groups.go` has no controller field/constructor. The owner-aware disk/
interface/LightVM helpers have no names/signatures; the VPP-ready flag has no
update path.

**Required fix:** define the controller constructor/dependencies, the
`api.Server` configuration method and call order, and exact internal helper
signatures (owner, desired state, ACLs, rollback).

### B12. Top-level Risks & Mitigations is missing

**Where:** §9 1013–1036; NFR 1184–1240; Structural S1.

Risks is an H3 inside Technical Plan, before Alternatives/NFR, with no impact/
mitigation structure. It mixes design rules, non-goals, and a duplicate capacity
bullet. NFR Security adds an unmitigated risk (unencrypted env/config).

**Required fix:** promote to `## Risks & Mitigations` after NFR; use
Risk/Impact/Mitigation; move design rules and non-goals back to their sections;
add env/config redaction as a risk.

--------------------------------------------------------------------------------

## Majors

| Finding | Where | Required fix |
| --- | --- | --- |
| `running_count` scans every member runtime on each read (including `/proc` fallback and QMP). A 1024-member group detail is a DoS vector. | 130–131, 436–437, 648, 1206–1211; Stage 7 D12; Stage 9 #11 | Compute from reconciled `lightvms.status`; reserve live `run_state` for a single member/action; cap or paginate members. |
| Image tag is mutable; a pinned version does not pin content. | 29–33, 576–582, 923–927; Stage 7 D14 | Resolve to a digest at version creation and store/display both, or document tag-only pinning and recommend digest references. |
| Terraform import cannot populate `current_version_id`; group import missing; force destroy does not poll to 404. | §8 989–990, 1008–1009; Phase 5 1412–1413; Stage 7 D15/Y22; Stage 9 #12 | Define import ID (`template_id/version_id` or latest/v1); add group import; poll normal/force destroy under `destroy_timeout`. |
| `config_files` is in the spec/schema but always rejected for image-backed templates. | 584, 689, 996, 1007; Style Y9; Stage 9 #13 | Remove from the template spec, Terraform schema, and UI, or mark reserved/rejected; adjust Security to env only. |
| Direct member delete is destructive and silently becomes “replace me”; no explicit member removal. | CUJ 4, UI 967–971; Stage 7 nit | Add the data-loss/replacement warning; state that only scale-in/group delete removes a slot, or add a member-removal action. |
| `delete_failed` retry rule conflicts with `DeleteDisk`/`MarkDiskDeleting`. | 813–817; `disk_deletion.go:19-29`; Stage 7 nit | Inspect `DeletionState` before calling delete, or add a store guard; do not reset attempts. |
| Attachment-mismatch repair is unavailable (“delete the extra attachment”) and the ownership sentence is logically wrong. | 803–806; Style Y18; Stage 7 nit | Define a supported repair (delete member workload → repair, or a scoped detach); say “do not delete resources the member does not own.” |
| Retryability classification is self-contradictory (“non-retryable … backoff”); “permanent Aborted” undefined. | 710–713; Style Y24 | Define terminal vs retryable, what a terminal error does to the slot, and how an operator clears it. |
| Fleet startup can race `reconcileLightVMStatuses` and overwrite a post-start running status with stopped. | 891–899; `reconcile.go:201-252`; Stage 7 nit | Sequence the fleet after initial status reconciliation, or skip members in that scan and let the worker repair status. |
| No deletion warning for referenced inventory. | 183–186, 921–927; Stage 7 nit | At minimum report “referenced by N template versions/groups” in delete responses/logs; no cross-cutting guards. |
| Test plan omits blocker cases and existing disk tests. | Phase 7 1436–1461; Stage 9 #16 | Add force-failing-delete, stopped-member repair, intent sync, attach ownership, total budget, mid-pass reset, LVM race, runtime-count cost, group import; update `disk_writes_test.go`, `disk_kouja_test.go`, `disk_deletion_test.go`. |
| Proto phase omits HTTP annotations and delete-response mapping. | Phase 1 1276–1288; Stage 9 #17 | Specify `google.api.http`, body/query (`?force=true`), and delete response types; verify OpenAPI/UI contract. |
| Interface cap 32 conflicts with 24 combined devices. | 687–688, 1237–1238; Stage 9 #18 | Use `min(32, 24 − disks − shares)` or remove 32; enforce at template validation. |

--------------------------------------------------------------------------------

## Structural and Editorial

*   **S1 (blocker):** top-level Risks missing.
*   **Decisions/Open Questions:** standalone Decisions restates Goals/Non-Goals/
    Technical Plan and displaces Open Questions. Fold normative decisions into
    Technical Plan; restore template order.
*   **Feature/plan duplication:** the 1270-line feature range includes full DDL,
    proto file tables, ACL function sequences, UI paths, Terraform contracts, and
    build/test commands; the plan repeats them. Move implementation inventories
    to the plan.
*   **Implementation detail:** `atomic.Uint64`, `sync.Mutex`, `resourceMu`/`runMu`,
    `FleetReconciler` signatures, and ACL helper names belong in the plan.
*   **ER cardinality:** `members ||--o{ lightvms` contradicts at most one; use
    `||--o|`.
*   **State diagram:** two `removing` boxes and only one inbound trigger; redraw
    with one node and all triggers (provisioning failure, missing/delete,
    scale-in).
*   **Revision note:** depends on `/tmp` and an untracked root report; remove or
    replace with a tracked changelog.
*   **Goals/Non-Goals:** 24/19 items are a mechanism backlog; keep 5–8 outcomes
    and scope exclusions.
*   **Limits:** 1024/32/24 and the false ten-action claim appear six times; use
    one normative table.
*   **Terminology:** “fleet” vs “fleet worker”; readiness generation/epoch/
    barrier; power intent/desired state; drift vs configuration divergence;
    kouja capitalization; title says “Instance Templates” while the body is
    LightVM-only; “no member `workload_id`” conflicts with the derived API field.
*   **CUJ 5:** two journeys (scale-in and delete); split and define `1/1`.
*   **Alternatives:** E/F/I/J/K restate non-goals; A/B/D restate the decision.
*   **Numbering:** Technical Plan §1–10, Alternatives A–Q, plan §1–9 make “§4”
    ambiguous.
*   **Overview:** repeats Problem/scope/data model; cut to 15–20 lines.

--------------------------------------------------------------------------------

## Nits

*   `member_name` is case-sensitive in SQL but validation says case-insensitive;
    generated names should be documented as non-input.
*   “Worker reports drift” contradicts computed-at-read.
*   `running_count` is in List responses but only computed in Get; state “0 in
    List” or compute consistently.
*   Member `template_version_id` is informational, indexed, and duplicated by
    `template_version`; drop the ID or document the reader.
*   `DeleteGroupResponse` fields are constant; remove or explain variation.
*   “Renaming does not affect ownership” does not say what is renamed.
*   “Resource names ≤ 63” conflicts with 64-character template/group names.
*   “Finalized”/“existing provisioning state” do not name disk statuses.
*   Unsupported throughput/risk claims (“without throughput,” “can exhaust the
    thin pool”).
*   Review-response residue (“No blocked state,” “no JSON ledger,” “Read is
    read-only,” “documented limitation,” “intentional,” “per-epoch”).
*   Mixed “Behavior/Behaviour,” range notation, and “Detailed Plan” vs “Detailed
    Implementation Plan.”
*   Repeated image-backed/kouja/capacity rules.
*   Boundary statements as observations rather than requirements.
*   “Paused” is overloaded (group, QEMU, stopped member).
*   “Force” naming fragmented (`force`, `force_delete`, `force_destroy`,
    “forced delete”).
*   “Open Questions: None” conflicts with unresolved force/empty, import,
    start/stop propagation, and update contract.

--------------------------------------------------------------------------------

## Revision-3 Traceability

| Revision-3 finding | Revision-4 status |
| --- | --- |
| B1 kouja FK blocks member delete | **Resolved** with `ON DELETE SET NULL`; **new** LVM provisioning/reclaimer race (B4). |
| B2 force unreachable | **Not resolved.** Force still does not enter `deleting`, can create replacements, and cannot finish on a failing LightVM delete (B1). |
| B3 pause/scale/count/blocked contradictions | **Partially resolved.** `blocked` removed; active-only scale-in; pause suppresses creates/restarts. Still: two count guards (B3), no total action budget (B8), intent transfer (B2), start-failure semantics (B6). |
| B4 generation latch | **Partially resolved.** Worker starts unconditionally and cleanup runs without VPP; still pass-scoped epoch and ungated starts (B9); no per-phase ready result. |
| B5 group requests/wake/ACL helper | **Resolved** (messages, `WakeFleet`/`FleetWake`, `SetACLVPPIndex` +1). Wiring of controller into API/serve is still undefined (B11). |
| B6 Terraform Read creates versions/schema/import/force | **Partially resolved.** Read-only Read, schema, normalization, force flag. Import/`current_version_id`, group import, destroy polling remain majors. |
| B7 ownership two sources of truth | **Resolved.** Member row has no workload ID; `lightvms.group_member_id` authoritative; partial unique index. Attach enforcement still missing (B7). |
| VM shell/boot content | **Removed by scope** (LightVM-only image-backed). |
| VM power race | **Removed by scope.** |
| VM/UI duplication | **Removed by scope.** |
| HTTP 412 mapping | **Resolved** with `FailedPrecondition` → 400 and `Aborted` → 409. |
| Counts computed at read | **Resolved** for `actual_count`/`drift`; `running_count` cost is a major. |
| Member update allowlist | **Resolved rule, but contradicts delete** (B5). |
| Worker timeout/mutex | **Partially resolved.** Per-member timeout exists; `resourceMu` image-pull block is a blocker (B10). |
| Capacity admission | **Accepted limitation.** |
| Sequential fleet work | **Accepted limitation.** |

--------------------------------------------------------------------------------

## What Is Sound

*   LightVM-only, image-backed scope is the right first release.
*   `group_member_id` on `lightvms` (RESTRICT), `disks`/`virtual_interfaces`
    (SET NULL) is the right ownership shape; the partial unique index is
    correct.
*   Member rows without `workload_id`/`workload_kind` remove the dual source of
    truth; recovery by owner is correct.
*   `actual_count`/`drift` at read and `running_count` in detail is the right
    split; only the runtime scan needs batching.
*   Four additive tables and three nullable columns; no destructive migration.
*   Two Terraform resources with inline spec, explicit version pin, and no
    version resources match the provider pattern.
*   The UI is confined to the LightVM tab; no VM inventory changes.

--------------------------------------------------------------------------------

## Recommendation

Write **revision 5** with five structural changes:

1.  **Member repair in place** when a LightVM disappears; keep the member row and
    its `desired_state`, recreate only resources/workload.
2.  **Atomic force**: state + flag in one transaction, force branch, owner clear
    on undeletable LightVM, separate cleanup context, empty force 204.
3.  **One tracked-count guard and one total action budget**; gate starts; add a
    stability window before clearing restart attempts.
4.  **Per-phase VPP epoch** with operation-level recheck; release `resourceMu`
    before image pull.
5.  **LVM create-then-insert**; attach ownership checks at the store; move
    implementation detail to the Detailed Plan and promote Risks.

Then run one final review. Do not implement revision 4; the blockers are the same
root cause, and line edits will not resolve them.

No production code has been changed.

--------------------------------------------------------------------------------

## Artifact Index

*   `docs/designs/instance-templates-and-groups.md` — revision 4
*   `zoom_out_report.md` — Stage 5
*   `comprehension_report.md` — Stage 6
*   `critic_report.md` — Stage 7
*   `intermediate_zoom_out_report.md` — Stage 8
*   `implementation_readiness_report.md` — Stage 9
*   `goldfish_review_report.md` — revision-4 remediation record
*   `/tmp/instance-templates-and-groups.revision3.md` — prior revision (outside
    the repository)
*   `/tmp/instance-templates-and-groups.reviewed-2026-11-24.md` — earlier
    reviewed copy (outside the repository)
