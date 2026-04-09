package viz

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"fishnet/internal/db"
)

// ─── Graph JSON format for D3 ─────────────────────────────────────────────────

type vizNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Summary     string `json:"summary"`
	CommunityID int    `json:"community"`
}

type vizEdge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Type   string  `json:"type"`
	Fact   string  `json:"fact"`
	Weight float64 `json:"weight"`
}

type vizGraph struct {
	Nodes []vizNode `json:"nodes"`
	Edges []vizEdge `json:"edges"`
}

// Serve starts a local HTTP server and opens the graph visualization.
// Returns the URL or an error.
func Serve(database *db.DB, projectID string) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, htmlTemplate)
	})
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		g, err := buildVizGraph(database, projectID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(g)
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	url := fmt.Sprintf("http://%s", ln.Addr())
	return url, nil
}

func buildVizGraph(database *db.DB, projectID string) (*vizGraph, error) {
	nodes, err := database.GetNodes(projectID)
	if err != nil {
		return nil, err
	}
	edges, err := database.GetEdges(projectID)
	if err != nil {
		return nil, err
	}

	g := &vizGraph{}
	for _, n := range nodes {
		g.Nodes = append(g.Nodes, vizNode{
			ID:          n.ID,
			Name:        n.Name,
			Type:        n.Type,
			Summary:     n.Summary,
			CommunityID: n.CommunityID,
		})
	}
	for _, e := range edges {
		g.Edges = append(g.Edges, vizEdge{
			Source: e.SourceID,
			Target: e.TargetID,
			Type:   e.Type,
			Fact:   e.Fact,
			Weight: e.Weight,
		})
	}
	return g, nil
}

// PrintASCII prints a simple ASCII summary of the graph to stdout.
func PrintASCII(database *db.DB, projectID string) error {
	nodes, err := database.GetNodes(projectID)
	if err != nil {
		return err
	}
	edges, err := database.GetEdges(projectID)
	if err != nil {
		return err
	}

	// Group by type
	byType := make(map[string][]db.Node)
	for _, n := range nodes {
		byType[n.Type] = append(byType[n.Type], n)
	}

	fmt.Printf("\n\033[1mGraph Overview\033[0m (%d nodes, %d edges)\n", len(nodes), len(edges))
	fmt.Println(strings.Repeat("─", 50))

	for typ, ns := range byType {
		fmt.Printf("\n\033[36m%s\033[0m (%d)\n", typ, len(ns))
		for i, n := range ns {
			if i >= 5 {
				fmt.Printf("  ... and %d more\n", len(ns)-i)
				break
			}
			summary := n.Summary
			if len(summary) > 60 {
				summary = summary[:60] + "..."
			}
			comm := ""
			if n.CommunityID >= 0 {
				comm = fmt.Sprintf(" [c%d]", n.CommunityID)
			}
			fmt.Printf("  • %s%s: %s\n", n.Name, comm, summary)
		}
	}

	// Show some edges
	fmt.Printf("\n\033[36mRelationships\033[0m (showing up to 10)\n")
	nodeByID := make(map[string]db.Node)
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}
	for i, e := range edges {
		if i >= 10 {
			fmt.Printf("  ... and %d more\n", len(edges)-i)
			break
		}
		src := nodeByID[e.SourceID].Name
		tgt := nodeByID[e.TargetID].Name
		fmt.Printf("  %s -[%s]-> %s\n", src, e.Type, tgt)
	}
	fmt.Println()
	return nil
}
