package provider

import "sync"

// Claude Code's result line reports total_cost_usd as a running total for
// the CLI session: it accumulates across turns in a long-lived streaming
// process, and it carries across --resume into a new process (both verified
// against captured fixtures, providertest/fixtures/claude). Usage.CostUSD is
// a per-event delta that consumers sum, so the adapter emits the difference
// from the previous result of the same session.
//
// The baseline lives in a process-wide ledger keyed by Claude session id
// rather than on the adapter: adapters are copied, cloned per session and
// sometimes shared, and the same session can be continued by a different
// adapter value. Session ids are UUIDs, so one ledger serves them all.
//
// Without a baseline, the result's own figures decide. modelUsage is
// cumulative like total_cost_usd, while usage covers this turn only; when
// they agree, this is the first turn the total covers, so the total is the
// delta. When modelUsage is larger, the session ran before this process saw
// it (a resume) and the earlier cost is unknown: the delta is reported as
// zero rather than charging the whole history to this turn. A turn that
// spends tokens on other models (a subagent) without a baseline is also
// reported as zero, for the same reason.
//
// ParseLine and ParseLineEvents both parse every line, so the delta is
// memoised per result uuid and both report the same figure.

const claudeCostLedgerCap = 1024

type claudeCostEntry struct {
	total    float64
	lastUUID string
	delta    float64
	seq      uint64
}

type claudeCostLedger struct {
	mu      sync.Mutex
	seq     uint64
	entries map[string]*claudeCostEntry
}

var claudeCosts = &claudeCostLedger{entries: map[string]*claudeCostEntry{}}

// delta returns the per-turn cost for one result line. firstCovered reports
// whether the line's cumulative figures cover only this turn.
func (l *claudeCostLedger) delta(sessionID, uuid string, total float64, firstCovered bool) float64 {
	if sessionID == "" || total <= 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e := l.entries[sessionID]
	if e != nil && uuid != "" && e.lastUUID == uuid {
		e.seq = l.seq
		return e.delta
	}
	var d float64
	switch {
	case e == nil && firstCovered:
		d = total
	case e == nil:
		d = 0
	case total >= e.total:
		d = total - e.total
	default:
		// The running total went down: the CLI started counting afresh.
		d = total
	}
	if e == nil {
		l.evictLocked()
		e = &claudeCostEntry{}
		l.entries[sessionID] = e
	}
	e.total, e.lastUUID, e.delta, e.seq = total, uuid, d, l.seq
	return d
}

// evictLocked drops the least recently used entry once the ledger is full.
func (l *claudeCostLedger) evictLocked() {
	if len(l.entries) < claudeCostLedgerCap {
		return
	}
	var oldestID string
	var oldest uint64
	for id, e := range l.entries {
		if oldestID == "" || e.seq < oldest {
			oldestID, oldest = id, e.seq
		}
	}
	delete(l.entries, oldestID)
}

// claudeModelUsage is one entry of a result line's modelUsage map, which is
// cumulative for the session.
type claudeModelUsage struct {
	InputTokens              int `json:"inputTokens"`
	OutputTokens             int `json:"outputTokens"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens"`
}

// claudeResultCost is the per-turn cost of a successful result line.
func claudeResultCost(ev claudeResultEvent) float64 {
	if ev.TotalCostUSD == nil {
		return 0
	}
	return claudeCosts.delta(ev.SessionID, ev.UUID, *ev.TotalCostUSD, claudeResultCoversOneTurn(ev))
}

// claudeResultCoversOneTurn reports whether the session's cumulative token
// counts (modelUsage, summed over models) equal this turn's (usage), so the
// cumulative cost is this turn's alone.
func claudeResultCoversOneTurn(ev claudeResultEvent) bool {
	if ev.Usage == nil || len(ev.ModelUsage) == 0 {
		return false
	}
	var sum claudeModelUsage
	for _, m := range ev.ModelUsage {
		sum.InputTokens += m.InputTokens
		sum.OutputTokens += m.OutputTokens
		sum.CacheReadInputTokens += m.CacheReadInputTokens
		sum.CacheCreationInputTokens += m.CacheCreationInputTokens
	}
	return sum.InputTokens == ev.Usage.InputTokens &&
		sum.OutputTokens == ev.Usage.OutputTokens &&
		sum.CacheReadInputTokens == ev.Usage.CacheReadInputTokens &&
		sum.CacheCreationInputTokens == ev.Usage.CacheCreationInputTokens
}
