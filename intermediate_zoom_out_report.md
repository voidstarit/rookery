# Stage 8: Intermediate Zoom Out Synthesis — Revision 4

*   **Target:** `docs/designs/instance-templates-and-groups.md` (1528 lines)
*   **Inputs:** `zoom_out_report.md`, `comprehension_report.md`,
    `critic_report.md`
*   **Mode:** Consolidated full-flow review

--------------------------------------------------------------------------------

## Verdict

**Not implementation-ready.** The LightVM-only scope removed the VM boot-content
problem and roughly half the surface, but four lifecycle contradictions remain
and one storage race is new. All three feature critics independently found the
same core issues, so this is a structural pass, not a set of line edits.

Blockers:

1.  **Force does not enter deletion** and can create replacements while trying to
    delete.
2.  **Force cannot complete** when `DeleteLightVM` cleanup fails, because the
    LightVM owner FK is `RESTRICT`.
3.  **Out-of-band replacement loses the stored power intent** because the member
    row is deleted before the replacement exists.
4.  **The member-insert count guard is defined two ways**, so a concurrent
    `desired_count` decrease can over-provision.
5.  **The LVM provisioning row is claimed by the reclaimer**, which can
    `lvremove` a volume that is still being created.
6.  **The allowed member action set excludes deletion**, which the design’s own
    CUJ 4 and recovery path require.

The structural reviewer adds a top-level section blocker: Risks is buried inside
Technical Plan and has no impact/mitigation structure.

--------------------------------------------------------------------------------

## Root Cause

The design treats a member as a disposable row plus a workload. Every time the
workload disappears, the row is cleaned up and a new row is created. That makes
power intent, ownership, force, and recovery four separate problems:

*   intent lives on the row being deleted;
*   ownership lives on the workload/resources that are being deleted;
*   force must bypass both the row and the workload;
*   recovery has no stable member identity to repair.

The same split appears in the worker: the API owns the epoch and loop, the
gateway owns the controller, and `canProvision` is computed once per pass. The
barrier is advisory, the action budget is partial, and starts are not gated.

The design also repeats the implementation plan inside the feature sections,
which hides the lifecycle ambiguity in DDL and pseudocode.

--------------------------------------------------------------------------------

## Recommended Revision 5 Direction

### 1. Make the member row the stable identity; repair the workload in place

Do not delete a member row just because its LightVM disappeared. Keep the member
row and its `desired_state`; mark the member `provisioning` for repair. Delete
the old owned disks/interfaces, create fresh ones, create a new LightVM with the
same `group_member_id`, and mark the member `active`. This preserves the stored
power intent, keeps `tracked` count stable, and still satisfies “fresh resources
after an out-of-band delete.” No replacement-row tombstone is needed.

Update CUJ 4, §4.3, §4.5, and the test plan to use “repair the member” rather
than “remove and create a replacement member.” Add a stopped-member out-of-band
delete test.

### 2. Make force an atomic deletion state with a working fallback

*   `DELETE ?force=true` sets `state='deleting'` and `force_delete=1` in one
    transaction, for both active and deleting groups.
*   The loop checks `state='deleting'` before any recovery or create; the member
    intent insert rejects `force_delete=1`.
*   Force uses a separate cleanup context and timeout. It attempts to stop and
    delete every owned LightVM.
*   If `DeleteLightVM` fails after QEMU is confirmed stopped, clear
    `lightvms.group_member_id` and leave the LightVM row in inventory for the
    operator, then delete the member/group. State that the orphan run-dir or
    container may remain.
*   Force on an empty group returns `204` synchronously; force on a non-empty
    group returns `202`.
*   Remove “never leaves an active workload” or make it “records any workload it
    could not delete and never deletes the group while one is owned.”

### 3. Make one action budget and gate starts

Define `fleetMaxActionsPerPass = 10` as the total of cleanup, scale-in,
restarts, and creates, processed in that order. A pass stops when the budget is
exhausted. Gate restarts on `canProvision`; a start during an outage is not
attempted. Use a separate cleanup context after a create/start deadline.

Add a stability window before clearing `restart_attempts` (for example, the
member must stay live for two passes). Otherwise a QEMU that starts and
immediately exits restarts every five seconds forever.

### 4. Make `canProvision` a per-phase epoch

Have `reconcileInfrastructure` return a dedicated `vppNetworkReady` result after
the network/vhost/ACL/route-table phases, not the joined error. Reset the
generation on the same `pendingReset` path used by `watchVPP`. Pass the epoch
token into create/interface/start calls and recheck before each external write.
The worker and controller can stay in their current packages if the token is
passed explicitly; the important part is operation-scoped rechecking.

### 5. Fix the LVM create path and the resource lock

*   Keep the existing LVM create-then-insert with rollback. Do not insert a
    `provisioning` row before `lvcreate` unless an lvm-thin reclaimer driver and
    a future claim time are added. The current generic reclaimer only has the
    kouja driver.
*   Release `resourceMu` before `PrepareLightVM`/image pull, or add a
    per-workload preparation lock. Do not hold the shared resource lock for a
    multi-minute image pull; the fleet multiplies it by ten.

### 6. Enforce ownership at every attach path

Add owner checks to `SetVMInterfaces`, `normalizeLightVMInterfaceIDs`,
`SetInterfaceAttachment`, `normalizeLightVMDiskIDs`, and the disk attach path.
Reject attaching a resource with non-null `group_member_id` to a workload whose
`group_member_id` differs. This is the only way “cleanup refuses to delete an
attached resource” does not become a permanent stall.

### 7. Define the member action set and update contract

*   Group members: reject `PUT /api/lightvm/instances/{id}` definition updates.
    Allow start, stop, console, and delete. Delete triggers member repair, not a
    group slot change; state the data-loss/replacement semantics in the UI.
*   `UpdateLightVMTemplate` is metadata-only. Spec changes use
    `CreateLightVMTemplateVersion`. The Terraform provider calls the version RPC
    on a spec diff and metadata update otherwise.
*   Remove `config_files` from the image-backed template spec, Terraform schema,
    and UI, or mark it reserved and always rejected.
*   Define template/spec normalization before storage, or a future default
    change mutates pinned versions.

### 8. Make runtime counts cheap and Terraform timeouts aligned

*   Compute `running_count` from stored LightVM status, not a live QMP scan per
    member. Reserve live `run_state` for a single member/detail action.
*   Use bulk teardown for group deletion/force, or raise the batch size and
    derive the provider `destroy_timeout` from it.
*   Define Terraform template import (`template_id/version_id` or latest/v1) and
    allow Read to populate state without creating a version.
*   Pin image content by digest at version creation or state explicitly that only
    the tag is pinned.

### 9. Structural cleanup

*   Promote Risks to a top-level `## Risks & Mitigations` with
    Risk/Impact/Mitigation.
*   Move DDL, proto tables, helper signatures, UI file paths, and verification
    commands to the Detailed Plan.
*   Remove the `/tmp` revision reference and review-response residue.
*   Fix the ER cardinality (`||--o|` for the workload), the state diagram (one
    `removing` node), and the title (“LightVM Templates and Groups”).
*   Collapse Goals/Non-Goals to outcomes and scope exclusions; one limits table;
    define fleet/readiness/power-intent/drift terms once.

--------------------------------------------------------------------------------

## What Is Already Sound

*   `group_member_id` on `lightvms`, `disks`, and `virtual_interfaces` is the
    right ownership shape. The partial unique index on `lightvms` is correct.
*   `ON DELETE SET NULL` for owned disks/interfaces solves the kouja member-row
    problem; `RESTRICT` on `lightvms` is correct for normal cleanup.
*   `actual_count`/`drift` computed at read and `running_count` in detail is the
    right split; the detail runtime scan needs batching, not a new count cache.
*   The sequential worker, per-member timeout, and persisted backoff are
    proportionate for the first release.
*   The LightVM-only image-backed scope removes the VM shell, VM power race, VM
    UI, and duplicate template surface.

--------------------------------------------------------------------------------

## Traceability

| Review finding | Revision 5 action |
| --- | --- |
| D1/Y3 force enters creates | Set deleting + force flag atomically; reject force in intent/creates. |
| D2 force cleanup fails | Clear workload owner after QEMU stop; force empty 204. |
| D3/D4/Y4/Y16 power intent lost/dual write | Repair member in place; member row authoritative; one start/stop protocol. |
| D5/Y2/Y18 action set | Reject definition updates; allow start/stop/console/delete. |
| D6 ownership attach | Store-level owner checks on every attach path. |
| D7/Y14 per-pass cap | One `fleetMaxActionsPerPass` for cleanup/scale-in/restarts/creates. |
| D8 start crash loop | Stability window before clearing attempts. |
| D9/Y15 VPP epoch/starts | Per-phase ready result; epoch token; gate starts. |
| D10 LVM reclaimer | Create-then-insert; no LVM provisioning row. |
| D11 resource lock | Release `resourceMu` before image pull. |
| D12 running_count scan | Stored status; live state only per member. |
| D13 Terraform timeout | Bulk teardown or aligned timeout. |
| D14 image tag | Digest pin or explicit tag-only limitation. |
| D15/Y22 import | Define import ID; Read populates state. |
| Y1 count guard | One tracked-count expression in insert and loop. |
| Y5 missing/not-live | Define two terms and use them. |
| Y6 cleanup context | Separate cleanup context/timeout. |
| Y7 force empty | 204 synchronous. |
| Y8 update contract | Metadata-only template PUT; version RPC for spec. |
| Y9 config_files | Remove or reserve. |
| Y10 display names | IDs only; names at read. |
| Y11/Y12 initial vs restart, kouja | Split rules; one kouja rule. |
| Y13 force absolute | Record failures; no false guarantee. |
| Y17 never replaced | “No automatic replacement while the workload exists.” |
| Y19/Y20 CUJ async/partial | Poll to converge; cleanup backoff. |
| Y25/Y26/Y27 kind/drift/normalization | Remove kind; reserve drift; normalize before store. |
| S1–S11 structure | Top-level Risks, plan split, ER/state fixes, title, terms, limits. |

--------------------------------------------------------------------------------

## Recommendation

Write revision 5 with the member-repair model, atomic force state, one action
budget, per-phase epoch, LVM create-then-insert, attach ownership checks, and the
structural cleanup. Do not run another review on revision 4; the remaining
issues are the same root cause, and line edits will not resolve them.

The first implementation should remain LightVM-only and image-backed.
