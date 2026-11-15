package projectroom

import "testing"

func TestThreadFiltersValidationAndBinding(t *testing.T) {
	for _, f := range []ThreadFilters{{Type: "bad"}, {VersionID: "bad"}, {CreatedAfter: "yesterday"}, {CreatedBefore: "today"}, {CreatedAfter: "2026-11-15T00:00:00Z", CreatedBefore: "2026-11-14T00:00:00Z"}} {
		if f.Validate() == nil {
			t.Fatalf("accepted %+v", f)
		}
	}
	for _, kind := range []string{"", "text", "component", "image", "page", "point"} {
		f := ThreadFilters{Type: kind}
		if err := f.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if (ThreadFilters{}).Binding("room") != "room" {
		t.Fatal("unfiltered cursor compatibility")
	}
	if (ThreadFilters{Type: "image"}).Binding("room") == (ThreadFilters{Type: "text"}).Binding("room") {
		t.Fatal("filter cursor collision")
	}
}
