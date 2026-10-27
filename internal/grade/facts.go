package grade

import (
	"fmt"
	"regexp"
	"strings"
)

type factKind int

const (
	factNumber factKind = iota + 1 // a number with a unit, a percent or a currency sign
	factDate
	factVersion
	factQuote
)

func (k factKind) String() string {
	switch k {
	case factNumber:
		return "number"
	case factDate:
		return "date"
	case factVersion:
		return "version"
	}
	return "quote"
}

// fact is a checkable claim in a research answer.
type fact struct {
	kind factKind
	text string   // as written: what to look for in the recorded pages
	alts []string // other spellings worth looking for
	key  string   // canonical form, to compare two answers
}

const (
	monthNames = `(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sept?(?:ember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)`
	numberExpr = `(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?`
	unitWords  = `percent|pct|ms|secs?|seconds?|mins?|minutes?|hrs?|hours?|days?|weeks?|months?|years?|yrs?|kb|mb|gb|tb|kib|mib|gib|bytes?|px|hz|khz|mhz|ghz|rps|qps|tps|bn|million|billion|thousand|trillion|users?|customers?|requests?|employees?|people|stars?|downloads?|commits?|usd|eur|dollars?|euros?|cents?`
)

var (
	quoteRe = regexp.MustCompile(`"([^"\n]{8,})"|“([^”\n]{8,})”`)
	dateRe  = regexp.MustCompile(`(?i)\b(?:\d{4}-\d{2}-\d{2}|` + monthNames + `\.?\s+\d{1,2}(?:st|nd|rd|th)?,?\s+\d{4}|\d{1,2}(?:st|nd|rd|th)?\s+` + monthNames + `\.?,?\s+\d{4}|` + monthNames + `\.?,?\s+\d{4})\b`)
	// v1.2.3, v2, 1.2.3, 1.2.3-beta.1
	versionRe = regexp.MustCompile(`(?i)\bv\d+(?:\.\d+){0,3}\b|\b\d+\.\d+\.\d+(?:\.\d+)?(?:[-+][0-9a-z.\-]+)?\b`)
	moneyRe   = regexp.MustCompile(`(?i)([$€£])\s?(` + numberExpr + `)(?:\s?(k|m|b|bn|million|billion|thousand|trillion)\b)?`)
	unitRe    = regexp.MustCompile(`(?i)(^|[^\w.$€£,])(` + numberExpr + `)(%|[kmbx]\b|\s?(?:` + unitWords + `)\b)`)

	isoRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	yearRe    = regexp.MustCompile(`\d{4}`)
	monthRe   = regexp.MustCompile(`jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec`)
	dayRe     = regexp.MustCompile(`\d{1,2}`)
	monthNums = map[string]string{"jan": "01", "feb": "02", "mar": "03", "apr": "04", "may": "05", "jun": "06",
		"jul": "07", "aug": "08", "sep": "09", "oct": "10", "nov": "11", "dec": "12"}

	unitAlias = map[string]string{"percent": "%", "pct": "%", "sec": "s", "second": "s", "minute": "min", "hour": "h",
		"hr": "h", "yr": "year", "dollar": "usd", "euro": "eur", "million": "m", "billion": "b", "bn": "b",
		"thousand": "k", "trillion": "t"}
)

// factsOf lists the checkable facts in text: quoted phrases (8+ characters),
// dates, version strings, and numbers with a unit, a percent sign or a currency
// sign. Bare numbers ("3 options", list numbers) are not facts. Text inside a
// quote, and URLs, are not read for other facts. Each fact is listed once.
func factsOf(text string) []fact {
	text = stripURLs(text)
	var out []fact
	seen := map[string]bool{}
	add := func(f fact) {
		if k := fmt.Sprintf("%d|%s", f.kind, f.key); f.key != "" && !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	text = quoteRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := quoteRe.FindStringSubmatch(m)
		inner := normSpace(sub[1] + sub[2])
		add(fact{kind: factQuote, text: inner, key: strings.ToLower(strings.TrimRight(inner, ".,;:!?"))})
		return " "
	})
	text = dateRe.ReplaceAllStringFunc(text, func(m string) string {
		add(fact{kind: factDate, text: normSpace(m), key: canonDate(m)})
		return " "
	})
	text = versionRe.ReplaceAllStringFunc(text, func(m string) string {
		add(fact{kind: factVersion, text: m, key: strings.TrimPrefix(strings.ToLower(m), "v")})
		return " "
	})
	text = moneyRe.ReplaceAllStringFunc(text, func(m string) string {
		s := moneyRe.FindStringSubmatch(m)
		code := map[string]string{"$": "usd", "€": "eur", "£": "gbp"}[s[1]]
		f := fact{kind: factNumber, text: normSpace(m), key: canonNumber(s[2]) + canonUnit(s[3]) + code}
		for _, v := range numberVariants(s[2]) {
			f.alts = append(f.alts, s[1]+v, v, v+" "+strings.ToUpper(code))
		}
		add(f)
		return " "
	})
	for _, m := range unitRe.FindAllStringSubmatch(text, -1) {
		num, written := m[2], m[3]
		unit := canonUnit(written)
		f := fact{kind: factNumber, text: normSpace(num + written), key: canonNumber(num) + unit}
		word := strings.TrimSpace(written)
		for _, v := range numberVariants(num) {
			f.alts = append(f.alts, v+word, v+" "+word)
			switch unit {
			case "%":
				f.alts = append(f.alts, v+"%", v+" percent")
			case "usd":
				f.alts = append(f.alts, "$"+v)
			case "eur":
				f.alts = append(f.alts, "€"+v)
			}
		}
		add(f)
	}
	return out
}

// numberVariants lists the spellings of a number: as written, without
// thousands commas, and with them.
func numberVariants(num string) []string {
	plain := strings.ReplaceAll(num, ",", "")
	out := []string{num, plain}
	whole, frac, hasFrac := strings.Cut(plain, ".")
	if len(whole) > 3 {
		var b strings.Builder
		for i, r := range whole {
			if i > 0 && (len(whole)-i)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteRune(r)
		}
		s := b.String()
		if hasFrac {
			s += "." + frac
		}
		out = append(out, s)
	}
	return out
}

// canonNumber drops thousands commas and trailing zeros after the point.
func canonNumber(s string) string {
	s = strings.ReplaceAll(s, ",", "")
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// canonUnit lowercases a unit, trims a plural "s" and maps spellings to one.
func canonUnit(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	if len(u) > 3 && strings.HasSuffix(u, "s") {
		u = u[:len(u)-1]
	}
	if a, ok := unitAlias[u]; ok {
		return a
	}
	return u
}

// canonDate writes a date as yyyy-mm-dd, or yyyy-mm when no day is given.
func canonDate(s string) string {
	s = strings.ToLower(s)
	if isoRe.MatchString(s) {
		return s
	}
	year, mon := yearRe.FindString(s), monthRe.FindString(s)
	if year == "" || mon == "" {
		return normSpace(s)
	}
	day := dayRe.FindString(strings.Replace(s, year, "", 1))
	if day == "" {
		return year + "-" + monthNums[mon]
	}
	if len(day) == 1 {
		day = "0" + day
	}
	return year + "-" + monthNums[mon] + "-" + day
}

// inPages reports whether a fact appears in a recorded page (in any of its spellings).
func inPages(rec Recording, f fact) bool {
	if rec.PagesContain(f.text) {
		return true
	}
	for _, a := range f.alts {
		if a != "" && rec.PagesContain(a) {
			return true
		}
	}
	return false
}
