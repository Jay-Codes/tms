package httpserver

import "testing"

func TestPageOfWalksTheWholeList(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	var got []int
	p := offsetPage{Limit: 2}
	for i := 0; i < 5; i++ {
		page, next := pageOf(items, p)
		got = append(got, page...)
		if next == nil {
			break
		}
		p.Offset += len(page)
	}
	if len(got) != 5 || got[4] != 5 {
		t.Fatalf("paged = %v, want every item once", got)
	}
	if page, next := pageOf(items, offsetPage{Offset: 9, Limit: 2}); len(page) != 0 || next != nil {
		t.Errorf("past the end = %v %v", page, next)
	}
}
