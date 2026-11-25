# Stage 6: Comprehension Test — Revision 4

*   **Target:** `docs/designs/instance-templates-and-groups.md` (1528 lines)
*   **Ranges:** Feature 1–1270; Detailed 1271–1528
*   **Mode:** Consolidated full-flow review
*   **Reviewer:** `egm-comprehension`

--------------------------------------------------------------------------------

**What this proposes**

This revision adds a LightVM-only fleet model: named LightVM templates whose versions hold an immutable `LightVMTemplateSpec`, and LightVM groups that pin one template version plus a `desired_count`. A bounded fleet worker in the API server creates one member per slot, gives each member its own blank managed data disks and vhost-user interfaces, restarts members whose stored power intent is `running`, scales in, and cleans up; global autostart skips group members. Ownership is a nullable `group_member_id` on `lightvms` (RESTRICT), `disks`, and `virtual_interfaces` (SET NULL), with member rows in `lightvm_group_members`. The release requires a non-empty container image, permits only blank managed data disks and read-only host shares, and defers VM templates and disk-backed LightVM groups.

**How the existing system works, per this document**

A LightVM is currently hand-assembled: the operator creates interfaces first and then passes their IDs to LightVM creation, which starts QEMU immediately and rolls the record back on failure. Interfaces are single-owner VPP vhost-user objects with generated MACs, network bindings, ACL assignments, and route-table defaults; disks are first-class inventory with a backend, size, throttle policy, and deletion rules, including asynchronous kouja deletion. API startup autostarts LightVMs whose `desired_state` is `running`, and `entity_labels` can group records for filtering but has no desired count or reconciliation. That baseline is the problem the fleet controller is meant to solve.

## Findings

1. **Major — Force has no loop path.** §4.4 826–834; §4.5 836–859; §4.7 882–889; §3.2 intent guard 544. §4.7 sets `force_delete=1` and follows §4.4, but §4.5 branches only on `state == 'deleting'` and never reads `force_delete`; the conditional insert checks only `state='active'`/`paused=0`. A force call on an active group has no documented path to mark members removing, delete the group, or stop new inserts unless the handler also sets `state='deleting'`, which is unstated. The error table’s “force not allowed” has no condition. State that force sets `state='deleting'` and add the force check to the loop and intent guard.

2. **Major — VPP gate boundary is ambiguous.** §4.1 731–739; §4.8 891–899; §4.4 step 2 810–812. §4.1 says cleanup/scale-in/delete run regardless but “interface writes require it,” then says a delete with no interfaces can finish during an outage. §4.8 says cleanup runs without VPP. It is unclear whether interface deletion is gated, and what happens to a deleting group with interfaces during an outage. State the exact gated operations and group-delete behavior; define “VPP identity” vs “generation/epoch.”

3. **Major — The per-pass cap claim is false.** §4.1 741; §4.4 821–824; §4.5 843–857; Performance 1206–1212. `fleetMaxMembersPerPass=10` caps only scale-in and creates; restarts, the deleting-branch marking, and cleanup are unbounded. A 100-member group can exceed the cap. Rename the limit to what it bounds or add an explicit action budget covering restarts and cleanup.

4. **Major — Intent guard and loop use different counts.** §4.2 768–770; §3.2 544; §4.5 847–855. The insert guard uses `desired_count > active_count`, while the loop uses `tracked = active + provisioning + removing`. A concurrent count decrease can overshoot because provisioning/removing are excluded from the insert guard. Name the authoritative count and use it in both places.

5. **Major — Group create is async but the contract is unstated.** §3.2 486–503; CUJ 1 229–238; group message 647–648; §8 wait 1008–1009; error table 707. `POST /groups` returns 201, members are created later; the document never states the create response has zero counts or how a client waits except Terraform’s opt-in flag. CUJ 1 reads as if members exist at create. The 503 “VPP not ready for a member create” maps to no HTTP endpoint. State the async contract and move/remove the 503 row.

6. **Major — Kouja and the SET NULL rationale are undefined.** Overview 56; §2.2 416–419; CUJ 8 295–303; §3.4 710–713; §4.4 813–819; Additional Resources 1249. “Kouja,” `storage.Reclaimer`, `202 deleting`, `delete_failed`, and `SESSION_ACTIVE` are not defined until a link far later. A fresh reader cannot tell why the member row must be removable before the volume is gone. Also, step 4 says delete the member row “when no owned resources remain,” but a kouja disk in `deleting` still carries `group_member_id` until the row delete triggers SET NULL; clarify that a queued deletion is not a blocker. Add a gloss/link at first use.

7. **Major — “Prepared-content path” is undefined.** Revision note 9–10; Non-Goals 163; Alternative E 1090–1096; Future 1259. VM templates are deferred “until a prepared-content path exists,” but the term is never defined (bootable image, preparation script, clone, content pipeline). Alternative E adds “VM power race” unexplained. Define or link the VM/disk provisioning design.

8. **Major — “Workload” is undefined.** §1 309–320; lines 52, 463, 776, 805, 1198. The document repeatedly says “the workload’s `group_member_id`” and “do not delete a workload the member does not own,” but *workload* is never defined. Disks/interfaces also carry the owner column, so it could mean any owned resource. Add “workload = the member’s LightVM (`lightvms` row)” to Terms and use it consistently.

9. **Major — Two desired_state fields have undefined precedence.** Overview 59–62; CUJ 3 248–256; member message 664–667; §4.3 796–799; plan 1357. The member row has `desired_state`, and the LightVM has `desired_state`. Recovery sets the LightVM from the member; create “sets the member’s desired state”; the plan says “power sync” without definition. It never says whether `/start` updates the member row. Specify the write path and precedence for `/start`/`/stop`; otherwise “one owner of member power” cannot be verified.

10. **Major — Reused LightVM messages and limits are not shown.** §3.3 573–599; §3.4 689. The spec reuses `LightVMEnvVar`, `LightVMConfigFile`, `LightVMConsole`, `LightVMVirtioFSShare`; validation says env/config limits match `CreateLightVM` and rejects non-zero runtime fields. The messages/limits are not shown or linked. “Non-zero” is wrong for string `socket`/`port_name` (non-empty). Add a link to `lightvm.proto` and an accepted/rejected field table.

11. **Major — Disk cleanup/attachment mismatch lacks a rule.** §4.3 803–806; §4.4 810–817; manual step 11 1502–1503. Cleanup refuses to delete an interface attached elsewhere and leaves the member removing, but for disks it only says delete by owner and “LVM/host deletes are synchronous.” A group disk can be attached elsewhere after an out-of-band LightVM delete; the doc does not say skip/fail/delete. The operator instruction “delete the extra attachment” is vague. State the disk rule with an example.

12. **Major — Cgroup hierarchy is undefined.** §3.4 685; §9 1029–1030. The rule says a class must exist and be a leaf, and the risk warns about adding a child that makes a pinned leaf non-leaf, but the document never explains slash-separated hierarchy, parent grouping, or leaf-only assignment. Add a one-line definition or link.

13. **Nit — Disk entry fields and host mention conflict.** Goals 115–117; `TemplateDiskEntry` 552–560; §3.4 687; §4.4 813. Host passthrough, read-only, and clone entries are rejected, but the entry has no source/read_only/template_disk_id fields. Cleanup’s “LVM/host deletes” implies host disks can exist. Say the entry is a subset and remove host-delete mention, or add the fields.

14. **Nit — 32 interfaces vs 24 combined cap.** §3.4 687–688; Limits 1237–1238. Storage caps combined disks/interfaces/shares at 24; interfaces allows 32. The 32 is unreachable. State the combined cap is effective or remove 32.

15. **Nit — Terraform import/current_version_id.** §8 989, 998. `current_version_id` is “the version this resource created,” but an imported template has none in state. Read uses it and import by template ID is supported; the doc doesn’t say whether import selects latest or fails. Specify.

16. **Nit — UI version route parameter.** §7 940; §3.2 497; §6 914–916. UI uses `{version}` while API uses `{version_id}`; the doc distinguishes version number from global version row ID but not the UI parameter. Name it.

17. **Nit — Undefined project terms.** `append` 588; §8 1005; cgroup 685; `ephemeral` 598–599; ACL helper 779; `aclIndexReference` 1374; throttle class 687. `DefaultLightVMAppend`, throttle class, ephemeral, and ACL +1 offset are not glossed. Add one-line glosses or links.

18. **Nit — VM kouja thin-pool change in a LightVM-only document.** Compatibility 1218–1220; Reliability 1203–1204. The section says kouja VMs no longer require the default thin pool (LightVM unchanged) and calls it the kouja thin-pool exception. This is a VM change in a LightVM-only scope and is unexplained. Explain why it belongs or remove it.

19. **Nit — Dead `config_files`.** Spec 584; validation 689. Config files are only for disk-backed definitions and are always rejected in group templates. Mark reserved/always empty or omit.

20. **Nit — “Version kind is LightVM”/“wrong kind.”** §3.4 692; error table 704. The design has one template type and defers VM templates; “kind” implies multiple. Remove or define as a future dimension.
