# Stage 7: Critic Reports — Revision 4

*   **Target:** `docs/designs/instance-templates-and-groups.md` (1528 lines)
*   **FeatureRange:** 1–1270
*   **Mode:** Consolidated full-flow review
*   **Reviewers:** `egm-design`, `egm-structural`, `egm-style`

--------------------------------------------------------------------------------

## Design Critic

### Blockers

**D1. Force does not leave the scale-out path.** §4.4 826–834, §4.5 838–859,
§4.7 884–888, Decisions 205–207, Invariants 435–438. Force sets
`force_delete=1` but not `state='deleting'`. The loop's teardown branch only runs
for `state='deleting'`, and the conditional member insert checks only
`state='active'`. A force request on an active group with `actual_count <
desired_count` can mark members `removing` and then create replacements.
**Fix:** set `state='deleting'` atomically with `force_delete=1`, and exclude
`force_delete` from creates/restarts and member intent.

**D2. Force cannot complete when `DeleteLightVM` cleanup fails.** §4.4 810–819,
826–834; §4.7 884–888; CUJ 8 295–303; `lightvm_instances.go:218-233`. Force
promises to delete every owned LightVM, but `lightvms.group_member_id` is
`RESTRICT`. A failing virtiofsd/container cleanup leaves the LightVM row and
therefore the member and group. **Fix:** add a force path that, once QEMU is
confirmed stopped, deletes the LightVM record or nulls `group_member_id` and
documents the orphan run-dir/container state; test a stuck QEMU.

**D3. Out-of-band replacement loses stored power intent.** Overview 59–62;
Goals 138–140; CUJ 4 258–265; §4.2 783–784; §4.5 854–856; §4.6 865–867.
Cleanup deletes the member row before a replacement is created; the replacement
uses the default `running` intent. A stopped member deleted out-of-band comes
back running. **Fix:** carry the old intent into the replacement row before
deleting the old member, or persist replacement intent on the group; test the
stopped-member case.

### Majors

**D4. Two copies of `desired_state` are written non-atomically.** Member row and
`lightvms.desired_state`; `/start`/`/stop` mutate the LightVM before member sync.
A crash between writes can cause restart of a stopped member or no restart of a
started member. Recovery can overwrite a concurrent `/start`. **Fix:** make the
member row authoritative, write it first/transactionally, and define one
recovery/start/stop protocol; test the interleavings.

**D5. “Only start/stop/console” contradicts CUJ 4 and recovery.** Goals 155–157;
validation 695; CUJ 4 258–265; §4.6 879. The design requires direct
`DELETE /api/lightvm/instances/{id}` and tells the operator to delete the
workload. **Fix:** state “reject definition updates; allow start/stop/console/
delete,” define delete semantics, and test delete-and-replace.

**D6. Group ownership is not enforced on the attach paths.** Ownership is added
to `disks`/`virtual_interfaces`, but the plan checks only `interface_writes.go`.
`SetVMInterfaces`, `normalizeLightVMInterfaceIDs`, `SetInterfaceAttachment`, and
disk attachment functions do not inspect `group_member_id`. A group resource can
be attached to an unrelated workload; cleanup then stalls, and force clears
ownership while the resource is still used. **Fix:** reject attaching any owned
resource to a different workload at every attach/update path; test VM and
LightVM attachment.

**D7. Per-pass action cap is false.** §4.1 741–744, §4.4 820–824, §4.5 852–856,
Performance 1207–1209. Only creates and scale-in marks are capped; cleanup and
restarts are unbounded, and each restart can consume the member timeout. A
1024-member group can monopolize the worker. **Fix:** apply one action budget to
cleanup, restarts, scale-in, and creates, or state the real worst case.

**D8. Start backoff clears on every successful start.** §4.6 865–878, §4.5
852–853. A process that starts and immediately exits (OOM, panic, cgroup kill)
resets `restart_attempts` each pass and restarts every five seconds forever.
**Fix:** require a stability window, or count rapid successive restarts and keep
backoff until the member stays alive; surface the crash loop.

**D9. `canProvision` is not derivable from the existing reconcile result.** §4.1
735–739; §4.8 891–899; `reconcile.go:58-99`. The design wants generation success
from the VPP network/interface/ACL/route-table subset, but
`reconcileInfrastructure` aggregates all phases into one `errors.Join`. Using the
whole pass lets a NAT44/VNF failure block fleet creates; using it loosely
provisions before vhost/ACL restore. `watchVPP` also sets `pendingReset` on ping
failure, not identity change. **Fix:** add per-phase success (a dedicated VPP
network-ready signal) and reset generation on the same pending-reset path.

**D10. LVM “provisioning row before volume create” conflicts with the
reclaimer.** §4.2 771–774; `storage/reclaimer.go:80-93`, `registry.go:24-26`.
`ClaimDueDiskDeletions` claims every `DiskDeletionProvisioning` row; the default
reclaimer registry only has the kouja driver, so an lvm-thin row becomes
`delete_failed`, and `FinalizeProvisioning` cannot complete. Kouja avoids this
with a 20-minute next-attempt. **Fix:** keep LVM create-then-insert with
rollback, or add an lvm-thin orphan cleaner and reclaim delay; test the crash
window.

**D11. Image pulls under `resourceMu` freeze interactive writes.** §4.1 741–748;
§4.2 775–777; `lightvm_instances.go:114-125,317`. `CreateLightVM` holds the
shared `resourceMu` through image preparation/pull; VM/disk/interface/network
writes share it. Ten creates per pass multiply the freeze. **Fix:** release
`resourceMu` before `PrepareLightVM`/image pull, or use a per-workload
preparation lock; document UI/API impact.

**D12. `running_count` makes group detail an unbounded runtime scan.** 130–131,
437–438, 648, 1206–1211; `qemu/lightvm.go:240-249`, `qemu.go:865-887`,
`lightvm_instances.go:884-901`. Detail returns every member and probes each
runtime, including `/proc` scans and QMP with a 2-second timeout. A 1024-member
group is a DoS vector. **Fix:** cache/batch runtime state, cap/paginate members,
or compute counts from stored status and refresh on a bounded background pass.

**D13. Terraform 30-minute destroy timeout does not match the worker.** §8 990,
1008–1009; Performance 1207–1209; §4.4 821–824. A 1024-member group needs 103
passes at ten removals per pass, before per-member cleanup; force uses the same
worker. **Fix:** bulk teardown for deletion/force, a larger batch, or an aligned
worst-case timeout documented in the provider.

**D14. Template version does not pin image content.** 29–33, 576–582, 923–927.
`image` is a mutable tag; a version can produce different members later.
**Fix:** resolve to a digest at version creation and store/display both, or
document that the version pins only the reference and recommend digests.

**D15. Terraform template import cannot work if `Read` is truly read-only.**
§8 989–990, 1013; tests 1053–1054. On import only the ID is set; a Read that does
not populate spec fields produces a plan that creates a duplicate version.
**Fix:** allow Read to populate state (but not create versions) and define which
version import binds to.

### Nits

*   Direct member delete is destructive and silently becomes “replace me”; add
    the data-loss/replacement warning and an explicit member-removal action or
    state that only scale-in/group delete removes a member (CUJ 4, UI 967–971).
*   “Do not retry `delete_failed`” conflicts with `DeleteDisk`/`MarkDiskDeleting`
    resetting attempts; inspect `DeletionState` or add a store guard (813–817,
    `disk_deletion.go:19-29`, `disk_writes.go:561-575`).
*   Attachment-mismatch recovery is ambiguous; the workload *is* owned, and
    “delete the extra attachment” may be unavailable. Define the comparison and
    a supported repair (803–806).
*   `config_files` is dead in an image-backed-only release; remove it from the
    spec, validation, Terraform, and UI (584, 689, 996).
*   `{version_id}` vs `{version}` is ambiguous; document the identifier and make
    UI/API match (497, 940).
*   The kouja VM thin-pool compatibility line is unrelated to LightVM groups;
    remove it (1218–1220).
*   Normalize defaults before storing a version, or a future default change
    mutates pinned behavior (360–361, 1005–1007).
*   Fleet startup can race `reconcileLightVMStatuses` and overwrite a post-start
    running status with stopped; sequence or skip members in the status scan
    (891–899, `reconcile.go:201-252`).
*   No deletion warnings for referenced inventory; at minimum report “referenced
    by N templates/groups” (183–186, 921–927).

--------------------------------------------------------------------------------

## Structural Critic

### Blocker

**S1. Top-level Risks & Mitigations is absent.** Risk content is an H3 inside
Technical Plan (“Risks and Operating Notes”), before Alternatives/NFR, with no
impact/mitigation structure. It mixes design rules, non-goals, and a duplicate
capacity bullet. NFR Security adds an unmitigated risk. **Fix:** promote to
`## Risks & Mitigations` after NFR, use Risk/Impact/Mitigation, move rules and
non-goals to their sections.

### Majors

**S2. Decisions/Open Questions placement and duplication.** A standalone
`## Decisions` restates Goals, Non-Goals, and Technical Plan and displaces the
required Open Questions order. **Fix:** fold normative decisions into the
Technical Plan or keep one concise section; restore the template order.

**S3. Feature range duplicates the detailed plan.** The 1270-line feature range
contains the migration filename and full DDL, proto file tables, ACL function
sequences, sixteen UI paths, Terraform contracts, and build/test commands; the
plan repeats them. **Fix:** keep architecture-level contracts in the design and
move DDL, file inventories, function sequences, UI paths, and commands to the
plan.

**S4. Architecture depends on implementation detail.** `atomic.Uint64`,
`sync.Mutex`, `resourceMu`/`runMu`, `FleetReconciler` signatures,
`EnsureVPPACL`/`SetACLVPPIndex`/`SetInterfaceACLs`, and the full pseudocode make
a fresh reader depend on internals. **Fix:** describe responsibilities and
ordering guarantees; move signatures/pseudocode to the plan.

**S5. ER cardinality contradicts the unique workload rule.** The diagram shows
`members ||--o{ lightvms`; §2.3 and the partial unique index enforce zero-or-one.
**Fix:** use `||--o|` and add a legend.

**S6. State diagram has two `removing` nodes and missing triggers.** It labels
only “operator deletes workload” for `active → removing`, while §4.5 also moves
on missing LightVM and scale-in. **Fix:** one `removing` node with all inbound
edges and a cleanup-complete exit.

**S7. Revision note depends on `/tmp` and an untracked root report.** It also
frames the document as a review response. **Fix:** remove it; keep
Author/Status/Date/Revision and put change history in the PR.

**S8. Goals and Non-Goals are mechanism lists.** 24 goals and 19 non-goals read
as a requirements backlog and duplicate the Technical Plan. **Fix:** five to
eight outcome goals; scope-only non-goals; one deferral list.

**S9. Limits are repeated six times and have drifted.** 1024, 32 interfaces, 24
combined devices, serial/compute rules, and the ten-action claim appear in
multiple sections; the ten-action claim is false. **Fix:** one normative limits
table; cross-reference it.

**S10. Terminology drift.** “Fleet” vs “fleet worker”; readiness
generation/epoch/barrier; power intent/desired state; drift vs configuration
drift; kouja capitalization; title says “Instance Templates” while the body is
LightVM-only; “no member `workload_id`” conflicts with the derived API field.
**Fix:** define each term once and use it consistently.

**S11. Unexplained kouja VM thin-pool change.** Compatibility claims a VM
behavior change not in goals or plan. **Fix:** remove or design it explicitly.

### Nits

*   CUJ 5 contains two journeys (scale-in and delete); split and define `1/1`.
*   Alternatives E/F/I/J/K restate non-goals; A/B/D restate the decision. Keep
    viable alternatives with independent failure modes.
*   Three numbering schemes (Technical Plan §1–10, Alternatives A–Q, plan §1–9)
    make “§4” ambiguous.
*   `?force` is shown as a separate endpoint row; proto names are bare; UI uses
    `{version}` while API uses `{version_id}`; `DeleteGroupResponse` sits in
    `template.proto`.
*   The Overview repeats Problem, scope, and data model; cut to 15–20 lines and
    move the template-name aside to Terms.

--------------------------------------------------------------------------------

## Style Critic

### Blockers

**Y1. The member-insert count guard is defined two ways.** §3.2 544–545 uses “a
count guard”; §4.2 768–770 uses `desired_count > active_count`; §4.5 847–856
uses `tracked = active + provisioning + removing`. A concurrent decrease can
over-provision. **Fix:** one expression such as
`COUNT(*) WHERE state IN ('active','provisioning','removing') < desired_count`,
evaluated in the insert transaction and used everywhere.

**Y2. “Only start/stop/console” excludes required deletion.** Same as D5.
**Fix:** reject definition updates; allow start/stop/console/delete; define
delete as replacement.

**Y3. Force on an active group is not reconciled.** Same as D1.
**Fix:** set `state='deleting'` with `force_delete=1` atomically, or add a force
branch before normal reconciliation; state the state in the 202 body.

**Y4. Replacement cannot read stored power intent.** Same as D3.
**Fix:** capture/transfer intent before deleting the member row.

### Majors

**Y5. “Missing” is undefined and can mean “no live process.”** §4.5 846, CUJ 2
240–246, §4.6 865, §4.3 794–798. If “missing” means no process, every crashed
member is marked removing, contradicting restart-only. **Fix:** define “missing
LightVM record” vs “not live”; use only those terms.

**Y6. Cleanup after a per-member deadline has no usable context.** The same
`context.WithTimeout` covers cleanup, but it has expired. **Fix:** separate
cleanup context/timeout and define cleanup timeout behavior.

**Y7. Force on an empty group is unspecified/contradictory.** Normal empty →
204; force → 202 with no empty branch. **Fix:** define force+empty as 204 (or a
single consistent result).

**Y8. `UpdateLightVMTemplate` has no contract.** §3.2 493, UI 972, Terraform 989.
Nothing says whether PUT accepts a spec or creates a version, or which RPC
Terraform calls. **Fix:** define metadata-only update vs
`CreateLightVMTemplateVersion`.

**Y9. `config_files` is simultaneously in the spec and forbidden.** 584, 689,
1189, 175. **Fix:** remove from this release or state reserved/rejected; adjust
Security to “env values only.”

**Y10. Display names are both “not stored” and “store IDs/names.”** 360–361,
696, 1193–1194. **Fix:** store IDs only; resolve names at read.

**Y11. Initial start failure and restart failure are conflated.** §2.4 463–465
says a start failure after the workload exists stays active; §4.2 785–787 says a
resource/workload failure after insert moves to removing. **Fix:** initial
create/start failure → removing; restart of an active member → active with
backoff.

**Y12. Kouja cleanup rules contradict each other.** Step 3 says delete the
member row without waiting; step 4 says only when no owned resources remain. CUJ
8 starts from delete_failed and requests deletion once, while §4.4 says don’t
retry deleting/delete_failed. **Fix:** one rule: member-row deletion is allowed
when the only remaining owned resources are kouja disks in
deleting/delete_failed (SET NULL); do not re-request for delete_failed.

**Y13. “Never leaves an active workload” is an unverified absolute.** Force
cannot guarantee QEMU termination. **Fix:** say “attempts to stop/delete every
owned LightVM; failures are recorded and retried,” and define verification
before group deletion.

**Y14. Three different per-pass limits.** Pseudocode caps creates/scale-in at
10; NFR says ten actions including restarts/cleanup; the risk says ten creates
can exhaust the pool (up to 240 disks). **Fix:** one shared action budget or
explicit separate caps; correct the risk.

**Y15. Restarts are not gated on `canProvision`.** §4.5 852–856, §4.3 797,
§4.1 736–739. **Fix:** gate starts, or explicitly state starts during outage
back off.

**Y16. `desired_state` belongs to two records; `/start`/`/stop` propagation is
unspecified.** **Fix:** qualify “member `desired_state`” vs “LightVM
`desired_state`” or rename the member column; define propagation and whether
stopped intent stops a live process.

**Y17. “Never replaced” contradicts operator-forced replacement.** §4.6
879–880, Non-Goals 167–168. **Fix:** “not replaced automatically while its
LightVM record exists; deleting that record triggers replacement.”

**Y18. Attachment-mismatch remedy is unavailable.** Same as D5/nit. **Fix:**
define a supported repair, and change “do not delete a workload the member does
not own” to “do not delete resources the member does not own.”

**Y19. CUJ 1 implies synchronous member creation.** Group create is async.
**Fix:** add “after the worker converges” or split create → poll counts.

**Y20. CUJ 7 “no partial resources remain” ignores cleanup backoff.** **Fix:**
state cleanup attempts/backoff and make the test poll to convergence.

**Y21. Interface caps conflict.** 32 vs 24 combined; 32 is unreachable. **Fix:**
effective cap `min(32, 24 − disks − shares)` or remove 32.

**Y22. Provider import cannot populate `current_version_id`.** **Fix:** define
the import ID (template/version) or resolve latest/v1 and state the mapping.

**Y23. Image-pull risk is both bounded and unbounded.** §9 says “up to the
member timeout” and “for the pull duration.” **Fix:** state lock waits are not
bounded by the member timeout; remove the first claim.

**Y24. Retryability classification is self-contradictory.** Non-retryable
conditions “set an error/backoff and do not recreate”; a backoff is a retry.
“Permanent `Aborted`” is undefined. **Fix:** define terminal vs retryable, what
a terminal error does to the slot, and how an operator clears it.

**Y25. “Version kind is LightVM” has no field.** §3.4 692, error table 704.
**Fix:** remove the rule or add a kind field.

**Y26. `drift` has two meanings.** Count difference vs configuration drift.
**Fix:** reserve drift for count; use “configuration divergence.”

**Y27. Normalization boundary is undefined.** Store-effective vs user values;
empty append/cgroup/consoles resolved at read vs provider sends defaults. **Fix:**
pick store-effective, normalize before write, compare the same representation.

**Y28. Kouja VM thin-pool bullet is unexplained scope creep.** **Fix:** remove
or design it explicitly.

**Y29. Revision note frames the design as a review response and depends on
external artifacts.** **Fix:** remove or replace with a factual changelog.

### Nits

*   `member_name` is case-sensitive in SQL but validation says case-insensitive;
    generated names should be documented as non-input.
*   ER cardinality `||--o{` contradicts at most one LightVM.
*   “Worker reports drift” contradicts computed-at-read.
*   `running_count` is in List but only computed in Get; state “0 in List” or
    compute it.
*   Member version ID is informational, indexed, and duplicated by version.
*   `DeleteGroupResponse` fields are constant.
*   “Renaming does not affect ownership” does not say what is renamed.
*   “Resource names ≤ 63” conflicts with 64-character template/group names.
*   “Finalized”/“existing provisioning state” do not name disk statuses.
*   Unsupported throughput/risk claims (“without throughput,” “can exhaust the
    thin pool”).
*   Review-response residue (“No blocked state,” “no JSON ledger,” “Read is
    read-only,” “documented limitation,” “intentional,” “per-epoch”).
*   Mixed “Behavior/Behaviour,” range notation, and “Detailed Plan” vs
    “Detailed Implementation Plan.”
*   Repeated image-backed/kouja/capacity rules.
*   Boundary statements as observations rather than requirements.
*   “Paused” is overloaded (group, QEMU, stopped member).
*   “Force” naming fragmented (`force`, `force_delete`, `force_destroy`,
    “forced delete”).
*   “Open Questions: None” conflicts with unresolved force/empty, import,
    start/stop propagation, and update contract.

--------------------------------------------------------------------------------

## Cross-Critic Convergence

The same four issues appear in all three feature reviews:

1.  **Force is not a working delete** (D1, D2, Y3, Y7, Y13).
2.  **Member power intent is not preserved** (D3, D4, Y4, Y16).
3.  **The allowed member action set is contradictory** (D5, Y2, Y18).
4.  **The ten-action cap and restart/cleanup behavior are not what the NFR
    claims** (D7, Y14, Y15).

Two more appear in design and style:

5.  **Group ownership is not enforced on attach** (D6).
6.  **The LVM provisioning row is claimed by the reclaimer** (D10).

The design needs a structural revision, not line edits.
