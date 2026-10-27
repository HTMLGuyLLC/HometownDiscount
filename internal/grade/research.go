package grade

import "strings"

// Research grades a "research" answer replayed on a recording.
//
// The key is the URLs the reference cites (normalized with NormalizeURL) and
// its checkable facts: numbers with units, percents or a currency sign, dates,
// version strings like v1.2.3, and quoted phrases of 8+ characters. A reference
// fact only counts when it appears in a recorded page.
//
// It passes when source recall >= theta, fact recall >= theta and nothing is
// fabricated: a cited URL the recording lacks (not fetched, not in a search
// result), or a number, version or quote that is in no recorded page.
//
// Judgment calls:
//   - Every URL the reference cites is a key source, even one the recording
//     lacks (the plan filters facts by the pages, not sources).
//   - A fact matches when the candidate states it in any common spelling
//     ("$1,200" and "1200 USD", "4.5%" and "4.5 percent", "March 3, 2025" and
//     "2025-03-03"). A quote matches when its words appear in the candidate.
//   - With no sources in the reference, only facts count, and the other way
//     round. With neither the job can't be graded ("no-key").
//   - Recall is the lower of the two recalls, so Pass is Recall >= theta with no
//     fabrication. Extra counts the cited URLs the reference lacks.
//   - Dates are keys but are not fabrication-checked: an answer often states
//     today's date. Numbers the model computed itself will read as fabricated.
func Research(ref, cand string, rec Recording, theta float64) Result {
	refURLs, candURLs := citedURLs(ref), citedURLs(cand)

	var refFacts []fact
	for _, f := range factsOf(ref) {
		if rec == nil || inPages(rec, f) {
			refFacts = append(refFacts, f)
		}
	}
	if len(refURLs) == 0 && len(refFacts) == 0 {
		return ungraded("no-key")
	}

	candSet := setOf(candURLs)
	srcHit := 0
	for _, u := range refURLs {
		if candSet[u] {
			srcHit++
		}
	}
	candFacts := factsOf(cand)
	candKeys := map[string]bool{}
	for _, f := range candFacts {
		candKeys[f.kind.String()+"|"+f.key] = true
	}
	candText := strings.ToLower(normSpace(cand))
	factHit := 0
	for _, f := range refFacts {
		if (f.kind == factQuote && strings.Contains(candText, f.key)) || candKeys[f.kind.String()+"|"+f.key] {
			factHit++
		}
	}

	srcRecall, factRecall := ratio(srcHit, len(refURLs)), ratio(factHit, len(refFacts))
	res := Result{Recall: min(srcRecall, factRecall)}
	refSet := setOf(refURLs)
	for _, u := range candURLs {
		if !refSet[u] {
			res.Extra++
		}
	}
	if rec != nil {
		res.Fabricated = calibrate(unrecorded(rec, candURLs, candFacts), unrecorded(rec, refURLs, factsOf(ref)))
	}

	var failed []string
	if !atLeast(srcRecall, theta) {
		failed = append(failed, "source-recall")
	}
	if !atLeast(factRecall, theta) {
		failed = append(failed, "fact-recall")
	}
	if len(res.Fabricated) > 0 {
		failed = append(failed, "fabrication")
	}
	return verdict(res, failed)
}

// unrecorded lists cited URLs the recording lacks and facts (but dates) that
// are in no recorded page.
func unrecorded(rec Recording, urls []string, facts []fact) []string {
	var out []string
	for _, u := range urls {
		if !rec.HasURL(u) {
			out = append(out, "URL not in the recording: "+u)
		}
	}
	for _, f := range facts {
		if f.kind != factDate && !inPages(rec, f) {
			out = append(out, f.kind.String()+" in no recorded page: "+f.text)
		}
	}
	return out
}
