package graph

import "testing"

func TestResolver_ExactMatch(t *testing.T) {
	r := NewResolver()
	names := []string{"Elon Musk", "Elon Musk"}
	result := r.ResolveEntities(names)
	if result["Elon Musk"] != "Elon Musk" {
		t.Errorf("exact match: expected canonical 'Elon Musk', got %q", result["Elon Musk"])
	}
}

func TestResolver_FuzzyMatch(t *testing.T) {
	r := NewResolver()
	// "Musk" is a substring of "Elon Musk" so they should be merged.
	// The longer form "Elon Musk" should become canonical.
	names := []string{"Elon Musk", "Musk"}
	result := r.ResolveEntities(names)

	elonCanon := result["Elon Musk"]
	muskCanon := result["Musk"]
	if elonCanon != muskCanon {
		t.Errorf("fuzzy match: expected both to share a canonical, got %q and %q", elonCanon, muskCanon)
	}
	// Canonical should be the longer form
	if elonCanon != "Elon Musk" {
		t.Errorf("fuzzy match: expected canonical to be 'Elon Musk', got %q", elonCanon)
	}
}

func TestResolver_NoMerge(t *testing.T) {
	r := NewResolver()
	names := []string{"Apple Inc", "Google"}
	result := r.ResolveEntities(names)

	appleCanon := result["Apple Inc"]
	googleCanon := result["Google"]
	if appleCanon == googleCanon {
		t.Errorf("no-merge: 'Apple Inc' and 'Google' should NOT be merged, both got canonical %q", appleCanon)
	}
}

func TestResolver_NormalizeTitle(t *testing.T) {
	r := NewResolver()
	// "Dr. Smith" normalizes to "smith", "Smith" normalizes to "smith" — should merge.
	names := []string{"Dr. Smith", "Smith"}
	result := r.ResolveEntities(names)

	drSmithCanon := result["Dr. Smith"]
	smithCanon := result["Smith"]
	if drSmithCanon != smithCanon {
		t.Errorf("title normalize: expected 'Dr. Smith' and 'Smith' to merge, got %q and %q", drSmithCanon, smithCanon)
	}
}

func TestResolver_PrefixMatch(t *testing.T) {
	r := NewResolver()
	// "United States" is a substring of "United States of America"
	names := []string{"United States", "United States of America"}
	result := r.ResolveEntities(names)

	usCanon := result["United States"]
	usaCanon := result["United States of America"]
	if usCanon != usaCanon {
		t.Errorf("prefix match: expected 'United States' and 'United States of America' to merge, got %q and %q", usCanon, usaCanon)
	}
	// Canonical should be the longer form
	if usCanon != "United States of America" {
		t.Errorf("prefix match: expected canonical to be 'United States of America', got %q", usCanon)
	}
}
