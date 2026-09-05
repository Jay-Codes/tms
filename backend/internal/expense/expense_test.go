package expense

import "testing"

// TestSortGroupsOrdersByAmountThenName pins the order a chart is drawn in:
// biggest spend first, ties broken by name so two identical windows produce two
// identical charts rather than whatever order the database happened to return.
func TestSortGroupsOrdersByAmountThenName(t *testing.T) {
	groups := []Group{
		{Name: "Cleaning", Amount: 10_000},
		{Name: "Security", Amount: 50_000},
		{Name: "Utilities", Amount: 0},
		{Name: "Insurance", Amount: 10_000},
		{Name: "Repairs", Amount: 50_000},
	}
	SortGroups(groups)

	want := []string{"Repairs", "Security", "Cleaning", "Insurance", "Utilities"}
	for i, name := range want {
		if groups[i].Name != name {
			t.Fatalf("position %d = %q, want %q — order was %v", i, groups[i].Name, name, names(groups))
		}
	}
}

func names(groups []Group) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Name)
	}
	return out
}

func TestSortGroupsHandlesTheEmptyAndSingleCases(t *testing.T) {
	SortGroups(nil)
	one := []Group{{Name: "Only", Amount: 1}}
	SortGroups(one)
	if one[0].Name != "Only" {
		t.Fatalf("a single group was mangled: %v", one)
	}
}

// TestChangePct covers the one figure on the summary that can be a lie: a rise
// from an empty previous window is not a percentage, it is a first month.
func TestChangePct(t *testing.T) {
	cases := []struct {
		name              string
		current, previous int64
		want              *float64
	}{
		{name: "a doubling", current: 180_000, previous: 90_000, want: f(100)},
		{name: "a halving", current: 45_000, previous: 90_000, want: f(-50)},
		{name: "no change", current: 90_000, previous: 90_000, want: f(0)},
		{name: "a fall to nothing", current: 0, previous: 90_000, want: f(-100)},
		{name: "a first month", current: 180_000, previous: 0, want: nil},
		{name: "two empty months", current: 0, previous: 0, want: nil},
		{name: "rounded to one decimal", current: 100_000, previous: 300_000, want: f(-66.7)},
		{name: "rounded up", current: 200_000, previous: 300_000, want: f(-33.3)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ChangePct(tc.current, tc.previous)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("change = %v, want null", *got)
			case tc.want == nil:
				return
			case got == nil:
				t.Fatalf("change = null, want %v", *tc.want)
			case *got != *tc.want:
				t.Fatalf("change = %v, want %v", *got, *tc.want)
			}
		})
	}
}

func f(v float64) *float64 { return &v }

// TestDefaultCategoriesAreTheEightFromThePlan keeps the seeded vocabulary and
// its order honest: the list is a product decision (FLOWS 12.5), and the
// seeded sort_order is this slice's index.
func TestDefaultCategoriesAreTheEightFromThePlan(t *testing.T) {
	want := []string{
		"Repairs & maintenance", "Utilities", "Security", "Cleaning",
		"Taxes & levies", "Insurance", "Management fees", "Other",
	}
	if len(DefaultCategories) != len(want) {
		t.Fatalf("seeded %d categories, want %d: %v", len(DefaultCategories), len(want), DefaultCategories)
	}
	for i, name := range want {
		if DefaultCategories[i] != name {
			t.Errorf("category %d = %q, want %q", i, DefaultCategories[i], name)
		}
	}
}
