package host

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ACLRuleCounter contains explicit matches for one ACL rule.
type ACLRuleCounter struct {
	ACLIndex  uint32 `json:"acl_index"`
	RuleIndex uint32 `json:"rule_index"`
	Action    string `json:"action"`
	Matched   uint64 `json:"matched"`
}

// ACLDirectionCounters contains explicit ACL rule matches for one direction.
type ACLDirectionCounters struct {
	Matched    uint64           `json:"matched"`
	Denied     uint64           `json:"denied"`
	Permitted  uint64           `json:"permitted"`
	ACLIndexes []uint32         `json:"acl_indexes,omitempty"`
	Rules      []ACLRuleCounter `json:"rules,omitempty"`
}

// ACLInterfaceCounters contains ACL rule-match counters for one interface.
type ACLInterfaceCounters struct {
	Interface string               `json:"interface"`
	SwIfIndex uint32               `json:"sw_if_index"`
	Ingress   ACLDirectionCounters `json:"ingress"`
	Egress    ACLDirectionCounters `json:"egress"`
}

type aclLookupContext struct {
	swIfIndex  uint32
	input      bool
	aclIndexes []uint32
}

type aclRuleHit struct {
	aclIndex  uint32
	ruleIndex uint32
	action    uint32
	hitcount  uint64
}

type aclHitCounters struct {
	denied    uint64
	permitted uint64
	rules     []aclRuleHit
}

var (
	aclLookupContextPattern = regexp.MustCompile(`index (\d+):interface ACL sw_if_index: (\d+) is_input: (\d+), acl_indices: (.*)`)
	aclAppliedPattern       = regexp.MustCompile(`acl (\d+) rule (\d+) action (\d+).*hitcount (\d+)`)
)

// ACLInterfaceCounters returns per-interface ACL lookup-context hit counters.
// VPP's table hitcount counts packets that matched an explicit rule; packets
// that fall through to the implicit deny are not included.
func (v *VPPNetwork) ACLInterfaceCounters(ctx context.Context) ([]ACLInterfaceCounters, error) {
	interfaces, err := v.VPPInterfaces(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[uint32]string, len(interfaces))
	for _, iface := range interfaces {
		names[iface.SwIfIndex] = iface.Name
	}

	contextsOut, err := v.runCLI(ctx, "show", "acl-plugin", "lookup", "context")
	if err != nil {
		return nil, fmt.Errorf("show ACL lookup contexts: %w: %s", err, contextsOut)
	}
	contexts := parseACLLookupContexts(contextsOut)
	if len(contexts) == 0 {
		return []ACLInterfaceCounters{}, nil
	}

	tablesOut, err := v.runCLI(ctx, "show", "acl-plugin", "tables", "applied")
	if err != nil {
		return nil, fmt.Errorf("show ACL applied tables: %w: %s", err, tablesOut)
	}
	hits := parseACLHitCounters(tablesOut)

	byInterface := make(map[uint32]*ACLInterfaceCounters)
	for lcIndex, context := range contexts {
		name := names[context.swIfIndex]
		if name == "" {
			name = fmt.Sprintf("sw_if_index %d", context.swIfIndex)
		}
		entry := byInterface[context.swIfIndex]
		if entry == nil {
			entry = &ACLInterfaceCounters{Interface: name, SwIfIndex: context.swIfIndex}
			byInterface[context.swIfIndex] = entry
		}
		direction := &entry.Egress
		if context.input {
			direction = &entry.Ingress
		}
		direction.ACLIndexes = append(direction.ACLIndexes, context.aclIndexes...)
		if counter, ok := hits[lcIndex]; ok {
			direction.Denied += counter.denied
			direction.Permitted += counter.permitted
			direction.Matched += counter.denied + counter.permitted
			for _, rule := range counter.rules {
				action := "deny"
				if rule.action != 0 {
					action = "permit"
				}
				direction.Rules = append(direction.Rules, ACLRuleCounter{
					ACLIndex:  rule.aclIndex,
					RuleIndex: rule.ruleIndex,
					Action:    action,
					Matched:   rule.hitcount,
				})
			}
		}
	}

	result := make([]ACLInterfaceCounters, 0, len(byInterface))
	for _, entry := range byInterface {
		sort.Slice(entry.Ingress.ACLIndexes, func(i, j int) bool {
			return entry.Ingress.ACLIndexes[i] < entry.Ingress.ACLIndexes[j]
		})
		sort.Slice(entry.Egress.ACLIndexes, func(i, j int) bool {
			return entry.Egress.ACLIndexes[i] < entry.Egress.ACLIndexes[j]
		})
		sort.Slice(entry.Ingress.Rules, func(i, j int) bool {
			if entry.Ingress.Rules[i].ACLIndex == entry.Ingress.Rules[j].ACLIndex {
				return entry.Ingress.Rules[i].RuleIndex < entry.Ingress.Rules[j].RuleIndex
			}
			return entry.Ingress.Rules[i].ACLIndex < entry.Ingress.Rules[j].ACLIndex
		})
		sort.Slice(entry.Egress.Rules, func(i, j int) bool {
			if entry.Egress.Rules[i].ACLIndex == entry.Egress.Rules[j].ACLIndex {
				return entry.Egress.Rules[i].RuleIndex < entry.Egress.Rules[j].RuleIndex
			}
			return entry.Egress.Rules[i].ACLIndex < entry.Egress.Rules[j].ACLIndex
		})
		result = append(result, *entry)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Interface) < strings.ToLower(result[j].Interface)
	})
	return result, nil
}

func parseACLLookupContexts(out string) map[uint32]aclLookupContext {
	result := make(map[uint32]aclLookupContext)
	for _, line := range strings.Split(out, "\n") {
		match := aclLookupContextPattern.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		lcIndex, _ := strconv.ParseUint(match[1], 10, 32)
		swIfIndex, _ := strconv.ParseUint(match[2], 10, 32)
		input := strings.TrimSpace(match[3]) == "1"
		context := aclLookupContext{swIfIndex: uint32(swIfIndex), input: input}
		for _, token := range strings.FieldsFunc(match[4], func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		}) {
			value, err := strconv.ParseUint(token, 10, 32)
			if err == nil {
				context.aclIndexes = append(context.aclIndexes, uint32(value))
			}
		}
		result[uint32(lcIndex)] = context
	}
	return result
}

func parseACLHitCounters(out string) map[uint32]aclHitCounters {
	result := make(map[uint32]aclHitCounters)
	var current uint32
	var haveCurrent bool
	for _, rawLine := range strings.Split(out, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "lc_index ") {
			value := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "lc_index ")), ":")
			lcIndex, err := strconv.ParseUint(value, 10, 32)
			if err == nil {
				current = uint32(lcIndex)
				haveCurrent = true
			}
			continue
		}
		if !haveCurrent {
			continue
		}
		match := aclAppliedPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		aclIndex, _ := strconv.ParseUint(match[1], 10, 32)
		ruleIndex, _ := strconv.ParseUint(match[2], 10, 32)
		action, _ := strconv.ParseUint(match[3], 10, 32)
		hitcount, _ := strconv.ParseUint(match[4], 10, 64)
		counter := result[current]
		counter.rules = append(counter.rules, aclRuleHit{
			aclIndex:  uint32(aclIndex),
			ruleIndex: uint32(ruleIndex),
			action:    uint32(action),
			hitcount:  hitcount,
		})
		if action == 0 {
			counter.denied += hitcount
		} else {
			counter.permitted += hitcount
		}
		result[current] = counter
	}
	return result
}
