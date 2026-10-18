package host

import "testing"

func TestParseVPPNodeGraph(t *testing.T) {
	output := `digraph {
  rankdir="LR"
  nodesep=2
  subgraph cluster_legend {
    label="Legend"
    0 [label="No packet", fixedsize=true shape=circle]
    0 -> 1 -> 2 [style="invis"]
  }
  "ip4-input" [shape=circle
 width=2.50 fontsize=18.50 fixedsize=true
 fillcolor=2 style=filled colorscheme=ylorrd8
]
  "ip4-lookup" [shape=circle width=1.20 fontsize=14.60 fixedsize=true]
  "ip4-input" -> "ip4-lookup"
  "ip4-input" -> "ip4-lookup"
}`
	graph, err := parseVPPNodeGraph(output)
	if err != nil {
		t.Fatalf("parse graph: %v", err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want 2", graph.Nodes)
	}
	if graph.Nodes[0].ID != "ip4-input" || graph.Nodes[0].Width != 2.5 || graph.Nodes[0].Fill != 2 {
		t.Fatalf("first node = %+v", graph.Nodes[0])
	}
	if len(graph.Edges) != 1 || graph.Edges[0].Source != "ip4-input" || graph.Edges[0].Target != "ip4-lookup" {
		t.Fatalf("edges = %+v", graph.Edges)
	}
}
