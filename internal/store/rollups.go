package store

import "math"

// ValidateRollupContributions checks v2 buckets against the exact proofs that
// will be archived. Sum in session order, as the materializer does, so a stale
// edition snapshot cannot commit different totals from its retained proofs.
func ValidateRollupContributions(rollups []SessionRollup, proofs []ArchivedSession) error {
	type key struct{ workID, day, timezone string }
	totals := make(map[key]SessionRollup)
	for _, p := range proofs {
		k := key{p.WorkID, p.Day, p.Timezone}
		r := totals[k]
		r.ActiveSeconds += p.ActiveSeconds
		r.Pages += p.Pages
		r.ProgDelta += p.ProgDelta
		r.SessionCount++
		r.MeasuredActiveSeconds += p.MeasuredActiveSeconds
		r.MeasuredProgDelta += p.MeasuredProgDelta
		totals[k] = r
	}
	for _, r := range rollups {
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
		delete(totals, k)
	}
	if len(totals) != 0 {
		return ErrConflict
	}
	return nil
}
