package store

import "math"

// ValidateRollupContributions checks v2 buckets against the exact proofs that
// will be archived. Sum in session order, as the materializer does, so a stale
// edition snapshot cannot commit different totals from its retained proofs.
func ValidateRollupContributions(rollups []SessionRollup, proofs []ArchivedSession) error {
	return prepareRollupContributions(rollups, proofs, false)
}

// PrepareRollupContributions validates v2 buckets and fills their exact
// per-session millisecond totals. Existing rollups created before this
// evidence was stored remain nil and cannot certify a comparison.
func PrepareRollupContributions(rollups []SessionRollup, proofs []ArchivedSession) error {
	return prepareRollupContributions(rollups, proofs, true)
}

func prepareRollupContributions(rollups []SessionRollup, proofs []ArchivedSession, fill bool) error {
	type key struct{ workID, day, timezone string }
	totals := make(map[key]SessionRollup)
	exactTotals := make(map[key]int64)
	exactComplete := make(map[key]bool)
	for _, p := range proofs {
		if p.ComparisonActiveMs == nil {
			return ErrConflict
		}
		k := key{p.WorkID, p.Day, p.Timezone}
		r := totals[k]
		r.ActiveSeconds += p.ActiveSeconds
		r.Pages += p.Pages
		r.ProgDelta += p.ProgDelta
		r.SessionCount++
		r.MeasuredActiveSeconds += p.MeasuredActiveSeconds
		r.MeasuredProgDelta += p.MeasuredProgDelta
		if _, seen := exactComplete[k]; !seen {
			exactComplete[k] = true
		}
		if *p.ComparisonActiveMs < 0 {
			return ErrConflict
		}
		if exactComplete[k] {
			if exactTotals[k] > math.MaxInt64-*p.ComparisonActiveMs {
				exactComplete[k] = false
			} else {
				exactTotals[k] += *p.ComparisonActiveMs
			}
		}
		totals[k] = r
	}
	for i := range rollups {
		r := &rollups[i]
		k := key{r.WorkID, r.Day, r.Timezone}
		total, ok := totals[k]
		if !ok || r.SessionCount != total.SessionCount {
			return ErrConflict
		}
		for _, pair := range [][2]float64{
			{r.ActiveSeconds, total.ActiveSeconds},
			{r.Pages, total.Pages},
			{r.ProgDelta, total.ProgDelta},
			{r.MeasuredActiveSeconds, total.MeasuredActiveSeconds},
			{r.MeasuredProgDelta, total.MeasuredProgDelta},
		} {
			if pair[0] != pair[1] || math.IsNaN(pair[0]) || math.IsInf(pair[0], 0) {
				return ErrConflict
			}
		}
		var exact *int64
		if exactComplete[k] {
			value := exactTotals[k]
			exact = &value
		}
		if r.ComparisonActiveMs != nil && (exact == nil || *r.ComparisonActiveMs != *exact) {
			return ErrConflict
		}
		if fill {
			r.ComparisonActiveMs = exact
		}
		delete(totals, k)
	}
	if len(totals) != 0 {
		return ErrConflict
	}
	return nil
}
