package graph

import (
	"strings"
	"unicode"

	"github.com/agnivade/levenshtein"
	"fishnet/internal/db"
)

// Resolver deduplicates extracted entities by fuzzy-matching their names.
// Entities with similarity above threshold are merged into a canonical form.
type Resolver struct {
	threshold float64 // similarity threshold 0-1 (default 0.85)
}

func NewResolver() *Resolver {
	return &Resolver{threshold: 0.85}
}

// similarity returns 1 - (editDistance / maxLen), ranging 0 to 1.
func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	dist := levenshtein.ComputeDistance(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 1.0
	}
	return 1.0 - float64(dist)/float64(maxLen)
}

// normalizeForCompare strips titles, lowercases, removes punctuation.
func normalizeForCompare(name string) string {
	// Remove common titles
	titles := []string{"Mr.", "Mrs.", "Ms.", "Dr.", "Prof.", "Sir", "Lady", "Lord"}
	s := name
	for _, t := range titles {
		s = strings.ReplaceAll(s, t+" ", "")
		s = strings.ReplaceAll(s, t, "")
	}
	// Lowercase and remove non-alphabetic chars
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// ResolveEntities takes a list of entity names and returns a map:
// rawName → canonicalName (the most frequent/longest form).
// Names that are sufficiently similar are merged into the same canonical.
func (r *Resolver) ResolveEntities(names []string) map[string]string {
	// Group names into clusters
	type cluster struct {
		canonical string
		members   []string
	}
	var clusters []cluster

	for _, name := range names {
		normName := normalizeForCompare(name)
		bestCluster := -1
		bestSim := 0.0

		for i, c := range clusters {
			normCanon := normalizeForCompare(c.canonical)
			sim := similarity(normName, normCanon)
			// Also check if one is a suffix/prefix of the other (e.g., "Musk" vs "Elon Musk")
			if strings.Contains(normCanon, normName) || strings.Contains(normName, normCanon) {
				sim = 0.9 // boost similarity for substring match
			}
			if sim > bestSim && sim >= r.threshold {
				bestSim = sim
				bestCluster = i
			}
		}

		if bestCluster >= 0 {
			clusters[bestCluster].members = append(clusters[bestCluster].members, name)
			// Update canonical to the longest form (most descriptive)
			if len(name) > len(clusters[bestCluster].canonical) {
				clusters[bestCluster].canonical = name
			}
		} else {
			clusters = append(clusters, cluster{canonical: name, members: []string{name}})
		}
	}

	// Build output map
	result := make(map[string]string, len(names))
	for _, c := range clusters {
		for _, m := range c.members {
			result[m] = c.canonical
		}
	}
	return result
}

// MergeNodesInDB merges duplicate nodes in the database.
// For each (rawName → canonicalName) pair where they differ:
//   - Ensures canonical node exists
//   - Re-points all edges from rawName node to canonical node
//   - Deletes the rawName node
func MergeNodesInDB(database *db.DB, projectID string, nameMap map[string]string) (merged int, err error) {
	nodes, err := database.GetNodes(projectID)
	if err != nil {
		return 0, err
	}

	// Build name → node map
	nodeByName := make(map[string]db.Node, len(nodes))
	for _, n := range nodes {
		nodeByName[n.Name] = n
	}

	for rawName, canonical := range nameMap {
		if rawName == canonical {
			continue // same, no merge needed
		}
		rawNode, rawExists := nodeByName[rawName]
		canonNode, canonExists := nodeByName[canonical]
		if !rawExists || !canonExists {
			continue
		}
		// Re-point edges
		if err := database.ReplaceNodeInEdges(rawNode.ID, canonNode.ID); err != nil {
			continue
		}
		// Delete the raw node
		if err := database.DeleteNode(rawNode.ID); err != nil {
			continue
		}
		merged++
	}
	return merged, nil
}

// ResolveProjectEntities is a convenience function that:
// 1. Gets all node names from DB
// 2. Runs ResolveEntities on all names
// 3. Calls MergeNodesInDB with the result
// 4. Returns count of merged nodes
func ResolveProjectEntities(database *db.DB, projectID string, resolver *Resolver) (int, error) {
	nodes, err := database.GetNodes(projectID)
	if err != nil {
		return 0, err
	}

	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Name)
	}

	nameMap := resolver.ResolveEntities(names)
	return MergeNodesInDB(database, projectID, nameMap)
}
