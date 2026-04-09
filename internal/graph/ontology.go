package graph

import (
	"context"
	"fmt"
	"strings"

	"fishnet/internal/llm"
)

// maxOntologySample is the maximum number of characters sent to the LLM for
// ontology generation. MiroFish uses 50 000; we use 10 000 as a practical
// default that avoids exceeding typical context windows while still giving the
// LLM far more signal than the original 3 000.
const maxOntologySample = 10000

// OntologySchema defines entity types and relationship types for graph extraction.
// Generated once by LLM before document processing begins.
type OntologySchema struct {
	EntityTypes []EntityTypeDef `json:"entity_types"`
	EdgeTypes   []EdgeTypeDef   `json:"edge_types"`
}

type EntityTypeDef struct {
	Name        string   `json:"name"`        // PascalCase e.g. "Person"
	Description string   `json:"description"`
	Examples    []string `json:"examples"`
}

type EdgeTypeDef struct {
	Name        string `json:"name"`        // UPPER_SNAKE_CASE e.g. "WORKS_FOR"
	Description string `json:"description"`
	SourceType  string `json:"source_type"` // entity type name
	TargetType  string `json:"target_type"`
}

// domainSystemPrompt is used in Pass 1 to detect the domains covered by a document.
const domainSystemPrompt = `You are a document analyst. Identify the main domains and themes covered in the given document excerpt.
Return ONLY raw JSON with this structure (no markdown, no explanation):
{
  "domains": ["domain1", "domain2"],
  "key_actor_types": ["ActorType1", "ActorType2"],
  "summary": "one or two sentence summary of the document"
}`

const ontologySystemPrompt = `You are an expert knowledge graph designer for social simulation.
Your task is to define an ontology for a knowledge graph that will be used to simulate social media dynamics.

IMPORTANT: Only define entity types that represent REAL ACTORS that can post on social media:
- People (individuals, public figures, journalists, politicians)
- Companies (businesses, corporations, startups)
- Organizations (NGOs, think tanks, activist groups)
- Media outlets (newspapers, TV channels, online publications)
- Government bodies (agencies, departments, ministries)

DO NOT include abstract concepts, topics, events, or locations as entity types.

Return ONLY raw JSON with this structure (no markdown, no explanation):
{
  "entity_types": [
    {
      "name": "Person",
      "description": "An individual human actor who can post on social media",
      "examples": ["Elon Musk", "Joe Biden", "Jane Doe"]
    }
  ],
  "edge_types": [
    {
      "name": "WORKS_FOR",
      "description": "Person or org works for or is employed by another org",
      "source_type": "Person",
      "target_type": "Organization"
    }
  ]
}

Define 4-6 entity types and 5-8 edge types that best fit the document content.`

// defaultOntology returns a sensible fallback schema when LLM fails.
func defaultOntology() *OntologySchema {
	return &OntologySchema{
		EntityTypes: []EntityTypeDef{
			{
				Name:        "Person",
				Description: "An individual human actor who can post on social media",
				Examples:    []string{"journalist", "politician", "activist"},
			},
			{
				Name:        "Organization",
				Description: "A non-profit, NGO, think tank, or activist group",
				Examples:    []string{"Amnesty International", "ACLU"},
			},
			{
				Name:        "Company",
				Description: "A for-profit business or corporation",
				Examples:    []string{"Apple", "ExxonMobil"},
			},
			{
				Name:        "MediaOutlet",
				Description: "A news or media organization that publishes content",
				Examples:    []string{"New York Times", "BBC", "Breitbart"},
			},
			{
				Name:        "Government",
				Description: "A government body, agency, or department",
				Examples:    []string{"EPA", "White House", "Congress"},
			},
		},
		EdgeTypes: []EdgeTypeDef{
			{
				Name:        "WORKS_FOR",
				Description: "Person is employed by or works for an organization or company",
				SourceType:  "Person",
				TargetType:  "Organization",
			},
			{
				Name:        "AFFILIATED_WITH",
				Description: "Entity is associated with or affiliated with another entity",
				SourceType:  "Person",
				TargetType:  "Organization",
			},
			{
				Name:        "OPPOSES",
				Description: "Entity publicly opposes or is in conflict with another entity",
				SourceType:  "Person",
				TargetType:  "Person",
			},
			{
				Name:        "SUPPORTS",
				Description: "Entity publicly endorses or supports another entity",
				SourceType:  "Person",
				TargetType:  "Person",
			},
			{
				Name:        "COVERS",
				Description: "Media outlet regularly covers or reports on an entity",
				SourceType:  "MediaOutlet",
				TargetType:  "Person",
			},
		},
	}
}

// domainAnalysis is the result of Pass 1 domain detection.
type domainAnalysis struct {
	Domains       []string `json:"domains"`
	KeyActorTypes []string `json:"key_actor_types"`
	Summary       string   `json:"summary"`
}

// GenerateOntology asks the LLM to produce a domain-specific ontology using a
// two-pass approach that mirrors MiroFish's ontology_generator.py:
//
//   - Pass 1: Detect which domains and actor types the document covers.
//   - Pass 2: Design entity types and edge types tailored to those domains.
//
// Falls back to a default schema if either LLM call fails.
// The document sample is capped at maxOntologySample characters.
func GenerateOntology(ctx context.Context, llmClient *llm.Client, documentSample string) (*OntologySchema, error) {
	if len(documentSample) > maxOntologySample {
		documentSample = documentSample[:maxOntologySample]
	}

	// ── Pass 1: domain detection ─────────────────────────────────────────────
	var domains domainAnalysis
	err := llmClient.JSON(ctx, domainSystemPrompt,
		fmt.Sprintf("Identify the domains and key actor types in this document:\n\n%s", documentSample),
		&domains)
	if err != nil || len(domains.Domains) == 0 {
		// Can't detect domains; proceed with a generic hint
		domains.Domains = []string{"general"}
		domains.KeyActorTypes = []string{}
	}

	// ── Pass 2: ontology design ──────────────────────────────────────────────
	// Build a richer user message that includes domain context from Pass 1.
	var domainHint strings.Builder
	if len(domains.Domains) > 0 {
		domainHint.WriteString(fmt.Sprintf("Document domains: %s\n", strings.Join(domains.Domains, ", ")))
	}
	if len(domains.KeyActorTypes) > 0 {
		domainHint.WriteString(fmt.Sprintf("Key actor types detected: %s\n", strings.Join(domains.KeyActorTypes, ", ")))
	}
	if domains.Summary != "" {
		domainHint.WriteString(fmt.Sprintf("Document summary: %s\n", domains.Summary))
	}

	userMsg := fmt.Sprintf(
		"%s\nBased on this document sample, define an appropriate ontology for a social simulation knowledge graph:\n\n%s",
		domainHint.String(),
		documentSample,
	)

	var schema OntologySchema
	err = llmClient.JSON(ctx, ontologySystemPrompt, userMsg, &schema)
	if err != nil {
		// Fall back to default schema on LLM failure
		return defaultOntology(), nil
	}

	// Validate we got something useful; fall back if empty
	if len(schema.EntityTypes) == 0 || len(schema.EdgeTypes) == 0 {
		return defaultOntology(), nil
	}

	return &schema, nil
}

// ToPromptHint returns a compact string listing entity types and edge types,
// used to guide the entity extraction prompt in the builder.
func (s *OntologySchema) ToPromptHint() string {
	var sb strings.Builder

	sb.WriteString("\nONTOLOGY CONSTRAINTS:\n")
	sb.WriteString("Entity types (use ONLY these):\n")
	for _, et := range s.EntityTypes {
		sb.WriteString(fmt.Sprintf("  - %s: %s\n", et.Name, et.Description))
	}

	sb.WriteString("Relationship types (use ONLY these):\n")
	for _, et := range s.EdgeTypes {
		sb.WriteString(fmt.Sprintf("  - %s (%s → %s): %s\n",
			et.Name, et.SourceType, et.TargetType, et.Description))
	}
	sb.WriteString("Extract ONLY entities that are real social media actors (people, orgs, companies, media, govt).\n")

	return sb.String()
}
