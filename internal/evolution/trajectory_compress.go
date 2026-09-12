package evolution

import (
	"fmt"
)

// trajMessage is one compacted trajectory line prepared for LLM analysis.
// ToolStep marks an assistant message that invoked tools: the compression
// unit is that message plus every tool result following it, and the unit is
// never split.
type trajMessage struct {
	Role     string
	Content  string
	ToolStep bool
}

// CompressTrajectory fits a compacted trajectory into budgetChars while
// protecting the two information-dense regions: the head (the task statement)
// and the tail (the resolution/outcome). Everything between is dropped whole
// units at a time, where a unit is one assistant tool-call step together with
// the tool results it produced — a tool response is never separated from the
// call that generated it, and the chronological order of kept messages is
// preserved. A marker records how many messages were omitted.
//
// The function returns the kept messages and the number of messages dropped.
func CompressTrajectory(msgs []trajMessage, budgetChars, headKeep, tailKeep int) ([]trajMessage, int) {
	if len(msgs) == 0 || budgetChars <= 0 {
		return msgs, 0
	}
	total := 0
	for _, m := range msgs {
		total += len(m.Content) + 1
	}
	if total <= budgetChars {
		return msgs, 0
	}

	// Group messages into atomic units: an assistant message carrying tool
	// calls consumes the run of tool results that immediately follow it.
	type unit struct{ start, end int } // half-open interval into msgs
	var units []unit
	for i := 0; i < len(msgs); {
		start := i
		i++
		if msgs[start].ToolStep {
			for i < len(msgs) && msgs[i].Role == "tool" {
				i++
			}
		}
		units = append(units, unit{start, i})
	}

	unitChars := func(u unit) int {
		n := 0
		for _, m := range msgs[u.start:u.end] {
			n += len(m.Content) + 1
		}
		return n
	}

	// Split units into head / middle / tail. Head and tail never overlap.
	headCount := headKeep
	tailCount := tailKeep
	if headCount+tailCount > len(units) {
		headCount = len(units) / 2
		tailCount = len(units) - headCount
	}
	head := units[:headCount]
	tail := units[len(units)-tailCount:]
	middle := units[headCount : len(units)-tailCount]

	headChars, tailChars := 0, 0
	for _, u := range head {
		headChars += unitChars(u)
	}
	for _, u := range tail {
		tailChars += unitChars(u)
	}

	// Shrink the protection zones themselves if they overrun the budget:
	// first trim tail units from the front (keeping the final unit, which
	// carries the outcome), then head units from the back (keeping the first,
	// which carries the task statement).
	for headCount+tailCount > 1 && headChars+tailChars > budgetChars {
		if tailChars >= headChars && tailCount > 1 {
			tailCount--
			tailChars -= unitChars(units[len(units)-tailCount-1])
		} else if headCount > 1 {
			headCount--
			headChars -= unitChars(units[headCount])
		} else {
			break
		}
		head = units[:headCount]
		tail = units[len(units)-tailCount:]
		middle = units[headCount : len(units)-tailCount]
	}

	// Reserve room for the omission marker so that dropping middle units
	// still yields output within the budget. When the middle fits entirely
	// the reserve simply goes unused.
	const markerReserve = 200
	middleBudget := budgetChars - headChars - tailChars
	if len(middle) > 0 {
		middleBudget -= markerReserve
	}

	var kept []trajMessage
	keptFromMiddle := 0
	appendUnit := func(u unit) {
		for _, m := range msgs[u.start:u.end] {
			kept = append(kept, m)
		}
	}
	for _, u := range head {
		appendUnit(u)
	}
	if middleBudget > 0 {
		used := 0
		for _, u := range middle {
			c := unitChars(u)
			if used+c > middleBudget {
				break
			}
			used += c
			appendUnit(u)
			keptFromMiddle++
		}
	}
	dropped := 0
	for _, u := range middle[keptFromMiddle:] {
		dropped += u.end - u.start
	}
	if dropped > 0 {
		kept = append(kept, trajMessage{
			Role: "system",
			Content: fmt.Sprintf(
				"[... middle of trajectory omitted: %d messages dropped to fit the analysis budget; the head (task) and tail (resolution) are preserved ...]",
				dropped),
		})
	}
	for _, u := range tail {
		appendUnit(u)
	}

	return kept, dropped
}
