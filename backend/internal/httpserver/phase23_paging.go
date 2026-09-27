package httpserver

import (
	"net/url"
	"strconv"
	"strings"

	"tms/backend/internal/validate"
)

// Phase 23 — paging for the reports that were computed whole.
//
// These reports are assembled in Go (joined, classified, totalled) before
// anything can be filtered, so a keyset cursor in SQL would page the wrong set.
// They page the finished list instead: `cursor` is an opaque offset into it,
// `total` is its length, and totals are always over the whole list.

const (
	reportPageDefault = 100
	reportPageMax     = 500
)

type offsetPage struct {
	Offset int
	Limit  int
}

func parseOffsetPage(f validate.Fields, qs url.Values) offsetPage {
	page := offsetPage{Limit: reportPageDefault}
	if v := strings.TrimSpace(qs.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > reportPageMax {
			f.Add("limit", "must be between 1 and "+strconv.Itoa(reportPageMax))
		} else {
			page.Limit = n
		}
	}
	if v := strings.TrimSpace(qs.Get("cursor")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			f.Add("cursor", "malformed cursor")
		} else {
			page.Offset = n
		}
	}
	return page
}

// pageOf returns one page of a finished list and the cursor for the next.
func pageOf[T any](items []T, p offsetPage) ([]T, *string) {
	if p.Offset >= len(items) {
		return []T{}, nil
	}
	end := min(p.Offset+p.Limit, len(items))
	var next *string
	if end < len(items) {
		c := strconv.Itoa(end)
		next = &c
	}
	return items[p.Offset:end], next
}
