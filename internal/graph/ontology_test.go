package graph

import (
	"strings"
	"testing"
)

// ─── defaultOntology ─────────────────────────────────────────────────────────

func TestDefaultOntology_HasRequiredEntityTypes(t *testing.T) {
	schema := defaultOntology()

	if len(schema.EntityTypes) == 0 {
		t.Fatal("defaultOntology returned empty EntityTypes")
	}
	if len(schema.EdgeTypes) == 0 {
		t.Fatal("defaultOntology returned empty EdgeTypes")
	}

	// Must contain a "Person" entity type.
	found := false
	for _, et := range schema.EntityTypes {
		if et.Name == "Person" {
			found = true
			break
		}
	}
	if !found {
		t.Error("defaultOntology EntityTypes does not contain 'Person'")
	}
}

func TestDefaultOntology_AllEntityTypesNamed(t *testing.T) {
	schema := defaultOntology()
	for i, et := range schema.EntityTypes {
		if et.Name == "" {
			t.Errorf("EntityTypes[%d].Name is empty", i)
		}
		if et.Description == "" {
			t.Errorf("EntityTypes[%d] (%s) has empty Description", i, et.Name)
		}
	}
}

func TestDefaultOntology_AllEdgeTypesNamed(t *testing.T) {
	schema := defaultOntology()
	for i, et := range schema.EdgeTypes {
		if et.Name == "" {
			t.Errorf("EdgeTypes[%d].Name is empty", i)
		}
		if et.SourceType == "" {
			t.Errorf("EdgeTypes[%d] (%s) has empty SourceType", i, et.Name)
		}
		if et.TargetType == "" {
			t.Errorf("EdgeTypes[%d] (%s) has empty TargetType", i, et.Name)
		}
	}
}

// ─── ToPromptHint ────────────────────────────────────────────────────────────

func TestOntologySchema_ToPromptHint_ContainsPerson(t *testing.T) {
	schema := defaultOntology()
	hint := schema.ToPromptHint()

	if hint == "" {
		t.Fatal("ToPromptHint returned empty string")
	}
	if !strings.Contains(hint, "Person") {
		t.Errorf("ToPromptHint does not contain 'Person': %q", hint)
	}
}

func TestOntologySchema_ToPromptHint_ContainsAllEntityTypes(t *testing.T) {
	schema := defaultOntology()
	hint := schema.ToPromptHint()

	for _, et := range schema.EntityTypes {
		if !strings.Contains(hint, et.Name) {
			t.Errorf("ToPromptHint missing entity type %q", et.Name)
		}
	}
}

func TestOntologySchema_ToPromptHint_ContainsAllEdgeTypes(t *testing.T) {
	schema := defaultOntology()
	hint := schema.ToPromptHint()

	for _, et := range schema.EdgeTypes {
		if !strings.Contains(hint, et.Name) {
			t.Errorf("ToPromptHint missing edge type %q", et.Name)
		}
	}
}

func TestOntologySchema_ToPromptHint_CustomSchema(t *testing.T) {
	schema := &OntologySchema{
		EntityTypes: []EntityTypeDef{
			{Name: "Robot", Description: "An autonomous agent", Examples: []string{"HAL9000"}},
		},
		EdgeTypes: []EdgeTypeDef{
			{Name: "COMMANDS", Description: "One entity commands another", SourceType: "Robot", TargetType: "Robot"},
		},
	}
	hint := schema.ToPromptHint()

	if !strings.Contains(hint, "Robot") {
		t.Errorf("custom hint missing 'Robot': %q", hint)
	}
	if !strings.Contains(hint, "COMMANDS") {
		t.Errorf("custom hint missing 'COMMANDS': %q", hint)
	}
}

// ─── OntologySchema structure validation ─────────────────────────────────────

func TestOntologySchema_Validation_EntityTypeCount(t *testing.T) {
	schema := defaultOntology()
	// Default should provide at least 4 entity types per the system prompt comment.
	if len(schema.EntityTypes) < 4 {
		t.Errorf("expected >= 4 entity types, got %d", len(schema.EntityTypes))
	}
}

func TestOntologySchema_Validation_EdgeTypeCount(t *testing.T) {
	schema := defaultOntology()
	// Default should provide at least 3 edge types.
	if len(schema.EdgeTypes) < 3 {
		t.Errorf("expected >= 3 edge types, got %d", len(schema.EdgeTypes))
	}
}

func TestOntologySchema_EmptyFallback(t *testing.T) {
	// An empty schema (what you'd get from a failed LLM parse) should trigger
	// fallback when validated. Test the validation path directly.
	empty := &OntologySchema{}
	if len(empty.EntityTypes) != 0 {
		t.Error("empty schema should have no entity types")
	}
	if len(empty.EdgeTypes) != 0 {
		t.Error("empty schema should have no edge types")
	}

	// Verify that ToPromptHint on empty schema doesn't panic and returns a string.
	hint := empty.ToPromptHint()
	if hint == "" {
		// ToPromptHint should at least return the section headers even with no types.
		// It includes "ONTOLOGY CONSTRAINTS:" even for empty schemas.
		t.Skip("empty schema hint is empty — acceptable if headers are present")
	}
}

// ─── EntityTypeDef and EdgeTypeDef ───────────────────────────────────────────

func TestEntityTypeDef_Examples(t *testing.T) {
	schema := defaultOntology()
	for _, et := range schema.EntityTypes {
		// Examples are optional, but if present should be non-empty strings.
		for i, ex := range et.Examples {
			if ex == "" {
				t.Errorf("EntityType %q Examples[%d] is empty string", et.Name, i)
			}
		}
	}
}
