package search

import (
	"testing"
)

func TestRoutingTable_WebGeneral_StartsWithBraveWeb(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentWebGeneral)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("web_general route is empty")
	}
	if route[0] != ProviderBrave {
		t.Errorf("web_general should start with BraveWeb, got %v", route[0])
	}
}

func TestRoutingTable_WebGeneral_EndsWithGoogleWeb(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentWebGeneral)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("web_general route is empty")
	}
	last := route[len(route)-1]
	if last != ProviderGoogleWeb {
		t.Errorf("web_general should end with GoogleWeb as fallback, got %v", last)
	}
}

func TestRoutingTable_WebGeneral_DoesNotIncludeOpenAlex(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentWebGeneral)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for i, pid := range route {
		if pid == ProviderOpenAlex {
			t.Errorf("OpenAlex should NOT appear in web_general, found at index %d", i)
		}
	}
}

func TestRoutingTable_Academic_IncludesOpenAlexAndArxiv(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentAcademic)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	hasOpenAlex := false
	hasArxiv := false
	for _, pid := range route {
		if pid == ProviderOpenAlex {
			hasOpenAlex = true
		}
		if pid == ProviderArxiv {
			hasArxiv = true
		}
	}
	if !hasOpenAlex {
		t.Error("academic route should include OpenAlex")
	}
	if !hasArxiv {
		t.Error("academic route should include arXiv")
	}
}

func TestRoutingTable_Preprint_StartsWithArxiv(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentPreprint)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("preprint route is empty")
	}
	if route[0] != ProviderArxiv {
		t.Errorf("preprint should start with arXiv, got %v", route[0])
	}
}

func TestRoutingTable_BookGeneral_StartsWithGoogleBooks(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentBookGeneral)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("book_general route is empty")
	}
	if route[0] != ProviderGoogleBooks {
		t.Errorf("book_general should start with GoogleBooks, got %v", route[0])
	}
}

func TestRoutingTable_BookGeneral_UsesOpenLibraryAsFallback(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentBookGeneral)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) < 2 {
		t.Fatal("book_general route should have at least 2 providers")
	}
	if route[len(route)-1] != ProviderOpenlibrary {
		t.Errorf("book_general should end with OpenLibrary as fallback, got %v", route[len(route)-1])
	}
}

func TestRoutingTable_BookGeneral_DoesNotIncludeOpenAlex(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentBookGeneral)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for i, pid := range route {
		if pid == ProviderOpenAlex {
			t.Errorf("OpenAlex should NOT appear in book_general, found at index %d", i)
		}
	}
}

func TestRoutingTable_BookAcademicOA_StartsWithDOAB(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentBookAcademicOA)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("book_academic_oa route is empty")
	}
	if route[0] != ProviderDoab {
		t.Errorf("book_academic_oa should start with DOAB, got %v", route[0])
	}
}

func TestRoutingTable_BookPublicDomain_StartsWithGutendex(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentBookPublicDomain)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("book_public_domain route is empty")
	}
	if route[0] != ProviderGutendex {
		t.Errorf("book_public_domain should start with Gutendex, got %v", route[0])
	}
}

func TestRoutingTable_UnknownIntent_ReturnsError(t *testing.T) {
	table := DefaultRoutingTable()
	_, err := table.RouteFor("unknown_intent")
	if err == nil {
		t.Error("expected error for unknown intent")
	}
}

func TestRoutingTable_News_StartsWithTavily(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentNews)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("news route is empty")
	}
	if route[0] != ProviderTavily {
		t.Errorf("news should start with Tavily, got %v", route[0])
	}
}

func TestRoutingTable_Biomedical_StartsWithPubmed(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentBiomedical)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("biomedical route is empty")
	}
	if route[0] != ProviderPubmed {
		t.Errorf("biomedical should start with PubMed, got %v", route[0])
	}
}

func TestRoutingTable_DOIResolve_StartsWithCrossref(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentDOIResolve)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("doi_resolve route is empty")
	}
	if route[0] != ProviderCrossref {
		t.Errorf("doi_resolve should start with Crossref, got %v", route[0])
	}
}

func TestRoutingTable_OpenAccessResolve_StartsWithUnpaywall(t *testing.T) {
	table := DefaultRoutingTable()
	route, err := table.RouteFor(IntentOpenAccessResolve)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(route) == 0 {
		t.Fatal("open_access_resolve route is empty")
	}
	if route[0] != ProviderUnpaywall {
		t.Errorf("open_access_resolve should start with Unpaywall, got %v", route[0])
	}
}
