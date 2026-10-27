package grade

import (
	"reflect"
	"testing"
)

func researchRec() *fakeRec {
	return newRec(map[string]string{
		"https://docs.example.com/pricing":  "Acme pricing. Pro plan: $20 per month. 99.9% uptime SLA. Free for up to 3 projects. Enterprise: $1,200 per year.",
		"https://blog.example.com/releases": "Acme v2.4.1 shipped on March 3, 2025 with faster builds.",
		"https://bench.example.org/acme":    "In our tests p95 latency was 120 ms under load. Success rate 4.5% higher than v2.3.0.",
	}, "https://news.example.net/acme-review")
}

// refAnswer cites three pages and states six checkable facts, all in the recording.
const refAnswer = "Acme's Pro plan costs $20 per month and includes a 99.9% uptime SLA ([pricing](https://docs.example.com/pricing)).\n" +
	"Version v2.4.1 shipped on March 3, 2025 (https://blog.example.com/releases/). The vendor says it is \"free for up to 3 projects\".\n" +
	"Independent tests measured p95 latency of 120 ms (https://bench.example.org/acme?utm_source=newsletter).\n"

const (
	candPricing  = "The Pro plan is $20 a month with a 99.9% uptime SLA, per https://www.docs.example.com/pricing/ .\n"
	candRelease  = "The v2.4.1 release landed on 2025-03-03 (https://blog.example.com/releases). It is described as free for up to 3 projects.\n"
	candBench    = "Benchmarks show p95 latency of 120 ms: https://bench.example.org/acme\n"
	candAllFacts = "The Pro plan is $20 a month with a 99.9% uptime SLA. v2.4.1 landed on 2025-03-03, free for up to 3 projects, p95 latency 120 ms.\n"
)

func TestResearch(t *testing.T) {
	const theta = 0.7
	cases := []struct {
		name      string
		ref, cand string
		rec       Recording
		theta     float64
		want      want
	}{
		{
			name: "same sources and facts in other words and spellings",
			ref:  refAnswer, cand: candPricing + candRelease + candBench,
			rec: researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "missing one of three sources is 0.67 and fails 0.7",
			ref:  refAnswer, cand: candPricing + candRelease,
			rec: researchRec(), theta: theta,
			want: want{graded: true, reason: "source-recall", recall: 2.0 / 3},
		},
		{
			name: "the same answer passes 0.6",
			ref:  refAnswer, cand: candPricing + candRelease,
			rec: researchRec(), theta: 0.6,
			want: want{pass: true, graded: true, recall: 2.0 / 3},
		},
		{
			name: "all sources but half the facts",
			ref:  refAnswer,
			cand: "The Pro plan is $20 a month with a 99.9% uptime SLA and free for up to 3 projects. See https://docs.example.com/pricing, https://blog.example.com/releases and https://bench.example.org/acme.",
			rec:  researchRec(), theta: theta,
			want: want{graded: true, reason: "fact-recall", recall: 0.5},
		},
		{
			name: "both recalls low",
			ref:  refAnswer, cand: "I could not find anything.",
			rec: researchRec(), theta: theta,
			want: want{graded: true, reason: "source-recall, fact-recall", recall: 0},
		},
		{
			name: "a cited URL that is not in the recording is fabricated",
			ref:  refAnswer, cand: candPricing + candRelease + candBench + "More at https://fake.example.com/acme.\n",
			rec: researchRec(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, extra: 1, fab: []string{"URL not in the recording: https://fake.example.com/acme"}},
		},
		{
			name: "a URL that only a search returned is not fabricated",
			ref:  refAnswer, cand: candPricing + candRelease + candBench + "A review: https://news.example.net/acme-review\n",
			rec: researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1, extra: 1},
		},
		{
			name: "a number in no page is fabricated",
			ref:  refAnswer, cand: candPricing + candRelease + candBench + "It is also 45% cheaper than rivals.\n",
			rec: researchRec(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, fab: []string{"number in no recorded page: 45%"}},
		},
		{
			name: "a version in no page is fabricated",
			ref:  refAnswer, cand: candPricing + candRelease + candBench + "The next release is v9.9.9.\n",
			rec: researchRec(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, fab: []string{"version in no recorded page: v9.9.9"}},
		},
		{
			name: "a quote in no page is fabricated",
			ref:  refAnswer, cand: candPricing + candRelease + candBench + "The CEO calls it \"the best tool on the market\".\n",
			rec: researchRec(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, fab: []string{"quote in no recorded page: the best tool on the market"}},
		},
		{
			name: "a date is not fabrication-checked",
			ref:  refAnswer, cand: candPricing + candRelease + candBench + "As of 2026-10-26 nothing changed.\n",
			rec: researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "list numbers and bare numbers are not facts",
			ref:  refAnswer, cand: "1. " + candPricing + "2. " + candRelease + "3. " + candBench + "There are 3 options and 12 more.\n",
			rec: researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "reference facts that are in no page are not required",
			ref:  refAnswer + "It also costs $500 a year.\n",
			cand: candPricing + candRelease + candBench,
			rec:  researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "number spellings: 1200 USD for $1,200 and 4.5 percent for 4.5%",
			ref:  "Enterprise costs $1,200 per year, and success was 4.5 percent higher (https://docs.example.com/pricing, https://bench.example.org/acme).",
			cand: "Enterprise is 1200 USD a year and success rate is 4.5% higher. https://docs.example.com/pricing https://bench.example.org/acme",
			rec:  researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "date spellings: 3rd March 2025 for March 3, 2025",
			ref:  "It shipped on March 3, 2025 (https://blog.example.com/releases).",
			cand: "It shipped on the 3rd March 2025. https://blog.example.com/releases",
			rec:  researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "sources only",
			ref:  "See https://docs.example.com/pricing.",
			cand: "Pricing is described at https://docs.example.com/pricing/",
			rec:  researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "facts only",
			ref:  "The plan has a 99.9% uptime SLA and costs $20.",
			cand: "It costs $20 and promises 99.9% uptime.",
			rec:  researchRec(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "empty candidate",
			ref:  refAnswer, cand: "",
			rec: researchRec(), theta: theta,
			want: want{graded: true, reason: "source-recall, fact-recall", recall: 0},
		},
		{
			name: "reference without sources or facts can't be graded",
			ref:  "I could not find anything useful.", cand: candPricing,
			rec: researchRec(), theta: theta,
			want: want{reason: "no-key"},
		},
		{
			name: "reference whose only facts are in no page can't be graded",
			ref:  "It costs $500 a year.", cand: "It costs $500 a year.",
			rec: researchRec(), theta: theta,
			want: want{reason: "no-key"},
		},
		{
			name: "no recording skips the page checks",
			ref:  "It costs $500 a year (https://nowhere.example.com/x).",
			cand: "It costs $500 a year, says https://nowhere.example.com/x and https://other.example.com/y.",
			rec:  nil, theta: theta,
			want: want{pass: true, graded: true, recall: 1, extra: 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, Research(c.ref, c.cand, c.rec, c.theta), c.want)
		})
	}
}

func TestResearchAllFactsNoSources(t *testing.T) {
	// A reference with sources and a candidate that restates every fact but cites nothing.
	res := Research(refAnswer, candAllFacts, researchRec(), 0.7)
	check(t, res, want{graded: true, reason: "source-recall", recall: 0})
}

func TestFactsOf(t *testing.T) {
	keys := func(text string) []string {
		out := []string{}
		for _, f := range factsOf(text) {
			out = append(out, f.kind.String()+":"+f.key)
		}
		return out
	}
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"money with commas", "It costs $1,200 per year.", []string{"number:1200usd"}},
		{"money with a scale word", "Raised $2.5 million last year.", []string{"number:2.5musd"}},
		{"percent", "Growth was 4.50% and 12 percent.", []string{"number:4.5%", "number:12%"}},
		{"time and size units", "30 days, 45 minutes, 10 seconds, 500 MB, 120 ms.", []string{"number:30day", "number:45min", "number:10s", "number:500mb", "number:120ms"}},
		{"multiplier", "It is 3x faster.", []string{"number:3x"}},
		{"counts with a unit word", "Over 1,500 users and 20 requests.", []string{"number:1500user", "number:20request"}},
		{"bare numbers are not facts", "Version 14 has 3 options, see section 2.", []string{}},
		{"a hyphenated unit is not read", "A 30-day trial.", []string{}},
		{"numbers glued to words are not facts", "Use p95 and h264 and abc120ms.", []string{}},
		{"versions", "Upgrade from v1.2.3 to 2.0.1-beta.1 or v2.", []string{"version:1.2.3", "version:2.0.1-beta.1", "version:2"}},
		{"one date in four spellings", "On March 3, 2025, 3rd March 2025, 2025-03-03 and Mar 3 2025.", []string{"date:2025-03-03"}},
		{"month and year", "Since Sept 2025.", []string{"date:2025-09"}},
		{"quote", `He said "pricing is per seat" today.`, []string{"quote:pricing is per seat"}},
		{"curly quote", "He said “pricing is per seat.” today.", []string{"quote:pricing is per seat"}},
		{"short quotes are not facts", `He said "no" and "maybe".`, []string{}},
		{"numbers inside a quote are part of the quote", `The page says "up to 5 GB free".`, []string{"quote:up to 5 gb free"}},
		{"URL digits are ignored", "See https://example.com/v2/12345ms?x=99%", []string{}},
		{"each fact once", "$20 now, $20 later", []string{"number:20usd"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := keys(c.text); !reflect.DeepEqual(got, c.want) {
				t.Errorf("factsOf(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

// "5–8s" (five- to eight-year-olds) and "the 1990s" aren't seconds: a bare
// "s" after a number is too ambiguous to check.
func TestBareSIsNotAUnit(t *testing.T) {
	for _, f := range factsOf("Near-universal for 5–8s, as in the 1990s; a 30s timeout is common.") {
		if f.kind == factNumber {
			t.Errorf("unexpected number fact %q", f.text)
		}
	}
	if fs := factsOf("Requests time out after 30 seconds."); len(fs) != 1 || fs[0].kind != factNumber {
		t.Errorf("a spelled-out unit still counts: %+v", fs)
	}
}
