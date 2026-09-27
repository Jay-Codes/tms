package httpserver

import (
	"context"
	"sort"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// Phase 22 §22.5 — cash reports net of rent refunds.
//
// Each wrapper stands in for one collected-money query and subtracts the rent
// refunded on the same axis, in the period the money went back out. sqlc
// cannot plan the UNION form of these queries, so the subtraction lives here,
// in one place, and every report handler calls these instead of the queries.

func (s *Server) collectedPeriod(ctx context.Context, p sqlc.ReportCollectedPeriodParams) (int64, error) {
	collected, err := s.q.ReportCollectedPeriod(ctx, p)
	if err != nil {
		return 0, err
	}
	refunded, err := s.q.RefundedPeriod(ctx, sqlc.RefundedPeriodParams{OrgID: p.OrgID, FromTs: p.FromTs, ToTs: p.ToTs})
	return collected - refunded, err
}

func (s *Server) collectionsCollected(
	ctx context.Context, p sqlc.ReportCollectionsCollectedParams,
) ([]sqlc.ReportCollectionsCollectedRow, error) {
	rows, err := s.q.ReportCollectionsCollected(ctx, p)
	if err != nil {
		return nil, err
	}
	refunds, err := s.q.RefundedBuckets(ctx, sqlc.RefundedBucketsParams{
		Bucket: p.Bucket, OrgID: p.OrgID, FromTs: p.FromTs, ToTs: p.ToTs,
	})
	if err != nil || len(refunds) == 0 {
		return rows, err
	}
	byBucket := map[string]int{}
	for i, r := range rows {
		byBucket[r.BucketStart.Time.Format(dateLayout)] = i
	}
	for _, rf := range refunds {
		if i, ok := byBucket[rf.BucketStart.Time.Format(dateLayout)]; ok {
			rows[i].Collected -= rf.Refunded
			continue
		}
		rows = append(rows, sqlc.ReportCollectionsCollectedRow{BucketStart: rf.BucketStart, Collected: -rf.Refunded})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].BucketStart.Time.Before(rows[b].BucketStart.Time) })
	return rows, nil
}

func (s *Server) revenueCollectedDaily(
	ctx context.Context, p sqlc.RevenueCollectedDailyParams,
) ([]sqlc.RevenueCollectedDailyRow, error) {
	rows, err := s.q.RevenueCollectedDaily(ctx, p)
	if err != nil {
		return nil, err
	}
	refunds, err := s.q.RefundedDaily(ctx, sqlc.RefundedDailyParams{
		OrgID: p.OrgID, FromTs: p.FromTs, ToTs: p.ToTs, PropertyID: p.PropertyID,
	})
	if err != nil || len(refunds) == 0 {
		return rows, err
	}
	byDay := map[string]int{}
	for i, r := range rows {
		byDay[r.Day.Time.Format(dateLayout)] = i
	}
	for _, rf := range refunds {
		if i, ok := byDay[rf.Day.Time.Format(dateLayout)]; ok {
			rows[i].Amount -= rf.Amount
			continue
		}
		rows = append(rows, sqlc.RevenueCollectedDailyRow{Day: rf.Day, Amount: -rf.Amount})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Day.Time.Before(rows[b].Day.Time) })
	return rows, nil
}

func (s *Server) revenueCollectedByProperty(
	ctx context.Context, p sqlc.RevenueCollectedByPropertyParams,
) ([]sqlc.RevenueCollectedByPropertyRow, error) {
	rows, err := s.q.RevenueCollectedByProperty(ctx, p)
	if err != nil {
		return nil, err
	}
	refunds, err := s.q.RefundedByProperty(ctx, sqlc.RefundedByPropertyParams{
		OrgID: p.OrgID, FromTs: p.FromTs, ToTs: p.ToTs, PropertyID: p.PropertyID,
	})
	if err != nil || len(refunds) == 0 {
		return rows, err
	}
	byProp := map[string]int{}
	for i, r := range rows {
		byProp[db.UUIDString(r.PropertyID)] = i
	}
	for _, rf := range refunds {
		if i, ok := byProp[db.UUIDString(rf.PropertyID)]; ok {
			rows[i].Amount -= rf.Amount
			continue
		}
		rows = append(rows, sqlc.RevenueCollectedByPropertyRow{PropertyID: rf.PropertyID, Amount: -rf.Amount})
	}
	return rows, nil
}
