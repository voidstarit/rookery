package host

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// VPPGraphNode is one active node from VPP's packet-processing graph.
type VPPGraphNode struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Width    float64 `json:"width,omitempty"`
	FontSize float64 `json:"font_size,omitempty"`
	Fill     int     `json:"fill,omitempty"`
}

// VPPGraphEdge is one edge between active VPP graph nodes.
type VPPGraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// VPPGraph is the active VPP packet-processing graph.
type VPPGraph struct {
	Nodes       []VPPGraphNode `json:"nodes"`
	Edges       []VPPGraphEdge `json:"edges"`
	GeneratedAt time.Time      `json:"generated_at"`
}

var (
	vppGraphNodePattern = regexp.MustCompile(`^"([^"]+)"\s*\[(.*)\]$`)
	vppGraphEdgePattern = regexp.MustCompile(`^"([^"]+)"\s*->\s*"([^"]+)"(?:\s*\[.*\])?$`)
	vppGraphAttrPattern = regexp.MustCompile(`([A-Za-z_]+)=("[^"]*"|[^\s\]]+)`)
)

// VPPNodeGraph returns the active VPP packet-processing graph. VPP's
// `show vlib graphviz filter` command only emits nodes active since the last
// stats clear, plus their edges.
func (v *VPPNetwork) VPPNodeGraph(ctx context.Context) (VPPGraph, error) {
	out, err := v.runCLI(ctx, "show", "vlib", "graphviz", "filter")
	graph, parseErr := parseVPPNodeGraph(out)
	if err != nil {
		// runCLI treats some node names (for example error/invalid nodes) as
		// failed output. Accept a syntactically valid DOT graph anyway.
		if parseErr == nil && strings.Contains(out, "digraph {") {
			graph.GeneratedAt = time.Now().UTC()
			return graph, nil
		}
		return VPPGraph{}, fmt.Errorf("show VPP node graph: %w: %s", err, out)
	}
	if parseErr != nil {
		return VPPGraph{}, parseErr
	}
	graph.GeneratedAt = time.Now().UTC()
	return graph, nil
}

func parseVPPNodeGraph(output string) (VPPGraph, error) {
	nodes := make(map[string]VPPGraphNode)
	edges := make(map[string]VPPGraphEdge)
	// VPP's CLI pager splits long DOT lines across several output lines. Join
	// quoted statements until they are complete instead of assuming one
	// statement per physical line.
	var statement string
	processStatement := func(statement string) {
		if match := vppGraphEdgePattern.FindStringSubmatch(statement); match != nil {
			edge := VPPGraphEdge{Source: match[1], Target: match[2]}
			edges[edge.Source+"\x00"+edge.Target] = edge
			for _, id := range []string{edge.Source, edge.Target} {
				if _, ok := nodes[id]; !ok {
					nodes[id] = VPPGraphNode{ID: id, Label: id}
				}
			}
			return
		}
		match := vppGraphNodePattern.FindStringSubmatch(statement)
		if match == nil {
			return
		}
		node := VPPGraphNode{ID: match[1], Label: match[1]}
		for _, attr := range vppGraphAttrPattern.FindAllStringSubmatch(match[2], -1) {
			value := strings.Trim(attr[2], `"`)
			switch attr[1] {
			case "width":
				node.Width, _ = strconv.ParseFloat(value, 64)
			case "fontsize":
				node.FontSize, _ = strconv.ParseFloat(value, 64)
			case "fillcolor":
				node.Fill, _ = strconv.Atoi(value)
			}
		}
		nodes[node.ID] = node
	}

	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if statement == "" {
			if !strings.HasPrefix(line, `"`) {
				continue
			}
			statement = line
		} else {
			statement += " " + line
		}
		// A complete edge is a single physical line, while node attributes may
		// span several pager lines and end with the closing bracket.
		if vppGraphEdgePattern.MatchString(statement) ||
			vppGraphNodePattern.MatchString(statement) {
			processStatement(statement)
			statement = ""
		} else if len(statement) > 4096 {
			statement = ""
		}
	}

	graph := VPPGraph{
		Nodes: make([]VPPGraphNode, 0, len(nodes)),
		Edges: make([]VPPGraphEdge, 0, len(edges)),
	}
	for _, node := range nodes {
		graph.Nodes = append(graph.Nodes, node)
	}
	for _, edge := range edges {
		graph.Edges = append(graph.Edges, edge)
	}
	sort.Slice(graph.Nodes, func(i, j int) bool { return graph.Nodes[i].ID < graph.Nodes[j].ID })
	sort.Slice(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].Source == graph.Edges[j].Source {
			return graph.Edges[i].Target < graph.Edges[j].Target
		}
		return graph.Edges[i].Source < graph.Edges[j].Source
	})
	return graph, nil
}
