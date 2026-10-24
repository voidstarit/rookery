package host

import "testing"

func TestParseACLCounterOutput(t *testing.T) {
	contexts := parseACLLookupContexts(`index 0:interface ACL sw_if_index: 5 is_input: 1, acl_indices: 2
index 1:interface ACL sw_if_index: 5 is_input: 0, acl_indices: 2
`)
	if len(contexts) != 2 {
		t.Fatalf("contexts = %+v", contexts)
	}
	if !contexts[0].input || contexts[0].swIfIndex != 5 || len(contexts[0].aclIndexes) != 1 || contexts[0].aclIndexes[0] != 2 {
		t.Fatalf("input context = %+v", contexts[0])
	}
	if contexts[1].input || contexts[1].swIfIndex != 5 {
		t.Fatalf("output context = %+v", contexts[1])
	}

	hits := parseACLHitCounters(`Stats counters enabled for interface ACLs: 0
Applied lookup entries for lookup contexts
lc_index 0:
  lookup applied entries:
       0: acl 2 rule 0 action 0 bitmask-ready rule 0 mask type index: 1 colliding_rules: 1 collision_head_ae_idx 0 hitcount 212 acl_pos: 0
lc_index 1:
  lookup applied entries:
       0: acl 2 rule 0 action 0 bitmask-ready rule 0 mask type index: 1 colliding_rules: 1 collision_head_ae_idx 0 hitcount 34 acl_pos: 0
       1: acl 2 rule 1 action 1 bitmask-ready rule 1 mask type index: 1 colliding_rules: 1 collision_head_ae_idx 0 hitcount 7 acl_pos: 1
`)
	if hits[0].denied != 212 || hits[0].permitted != 0 {
		t.Fatalf("input hits = %+v", hits[0])
	}
	if hits[1].denied != 34 || hits[1].permitted != 7 {
		t.Fatalf("output hits = %+v", hits[1])
	}
	if len(hits[1].rules) != 2 || hits[1].rules[0].aclIndex != 2 || hits[1].rules[0].ruleIndex != 0 || hits[1].rules[0].action != 0 || hits[1].rules[0].hitcount != 34 {
		t.Fatalf("output rules = %+v", hits[1].rules)
	}
}
