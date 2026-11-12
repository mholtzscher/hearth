package mdns

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"
)

const maxBrowseInterval = time.Hour
const maxQueryBytes = 8192
const refreshFirstPercent = 80
const refreshStepPercent = 5
const refreshSteps = 4
const backoffFactor = 2

func nextBrowseInterval(interval time.Duration) time.Duration {
	if interval >= maxBrowseInterval/2 {
		return maxBrowseInterval
	}
	return interval * backoffFactor
}

func questionKey(q dns.Question) string { return fmt.Sprintf("%s|%d", name(q.Name), q.Qtype) }

func recordQuestion(rr dns.RR) string {
	return fmt.Sprintf("%s|%d", name(rr.Header().Name), rr.Header().Rrtype)
}

// newInstances only schedules follow-ups for newly learned PTR or SRV records;
// periodic refreshes must not reset the independent browse backoff.
func (c *recordCache) newInstances(msg *dns.Msg, scope string) map[string]bool {
	instances := make(map[string]bool)
	if !msg.Response || msg.Truncated {
		return instances
	}
	for _, records := range [][]dns.RR{msg.Answer, msg.Ns, msg.Extra} {
		for _, rr := range records {
			if rr.Header().Ttl == 0 || rr.Header().Class&0x7fff != dns.ClassINET || !supported(rr) {
				continue
			}
			if _, exists := c.records[recordKey(rr, scope)]; exists {
				continue
			}
			switch r := rr.(type) {
			case *dns.PTR:
				instances[name(r.Ptr)] = true
			case *dns.SRV:
				key := name(r.Hdr.Name)
				if _, exists := instances[key]; !exists {
					instances[key] = false
				}
			}
		}
	}
	return instances
}

//nolint:gocognit // Follow-ups require the PTR association and each missing component.
func (c *recordCache) missingQuestions(scope string, instances map[string]bool) []dns.Question {
	associated := make(map[string]bool)
	byQuestion := make(map[string][]dns.RR)
	for _, entry := range c.records {
		if entry.scope != scope {
			continue
		}
		byQuestion[recordQuestion(entry.rr)] = append(byQuestion[recordQuestion(entry.rr)], entry.rr)
		if ptr, ok := entry.rr.(*dns.PTR); ok {
			associated[name(ptr.Ptr)] = true
		}
	}
	var questions []dns.Question
	seen := make(map[string]bool)
	addMissing := func(host string, typ uint16) {
		q := dns.Question{Name: host, Qtype: typ, Qclass: dns.ClassINET}
		key := questionKey(q)
		if len(byQuestion[key]) == 0 && !seen[key] {
			questions = append(questions, q)
			seen[key] = true
		}
	}
	for instance, discovered := range instances {
		if !associated[instance] {
			continue
		}
		if discovered {
			addMissing(instance, dns.TypeSRV)
			addMissing(instance, dns.TypeTXT)
		}
		for _, record := range byQuestion[questionKey(dns.Question{Name: instance, Qtype: dns.TypeSRV})] {
			srv, ok := record.(*dns.SRV)
			if !ok {
				continue
			}
			if srv.Port == 0 || !strings.HasSuffix(name(srv.Target), ".local.") {
				continue
			}
			host := name(srv.Target)
			a := questionKey(dns.Question{Name: host, Qtype: dns.TypeA})
			aaaa := questionKey(dns.Question{Name: host, Qtype: dns.TypeAAAA})
			if len(byQuestion[a]) == 0 && len(byQuestion[aaaa]) == 0 {
				addMissing(host, dns.TypeA)
				addMissing(host, dns.TypeAAAA)
			}
		}
	}
	return questions
}

type missingRetry struct {
	next     time.Time
	interval time.Duration
}

func (c *recordCache) canAskMissing(scope, key string, now time.Time) bool {
	retry, ok := c.missingLast[scope+"|"+key]
	return !ok || !now.Before(retry.next)
}

func (c *recordCache) markMissing(scope, key string, now time.Time) error {
	key = scope + "|" + key
	retry, exists := c.missingLast[key]
	if !exists && len(c.missingLast) >= maxCacheRecords {
		return fmt.Errorf("mDNS missing question limit (%d) exceeded", maxCacheRecords)
	}
	if exists {
		retry.interval = nextBrowseInterval(retry.interval)
	} else {
		retry.interval = time.Second
	}
	retry.next = now.Add(retry.interval)
	c.missingLast[key] = retry
	return nil
}

// wantedMissing is restricted to incomplete, currently associated services.
func (c *recordCache) wantedMissing() map[string]bool {
	instances := make(map[string]map[string]bool)
	for _, entry := range c.records {
		if ptr, ok := entry.rr.(*dns.PTR); ok {
			if instances[entry.scope] == nil {
				instances[entry.scope] = make(map[string]bool)
			}
			instances[entry.scope][name(ptr.Ptr)] = true
		}
	}
	wanted := make(map[string]bool)
	for scope, names := range instances {
		for _, question := range c.missingQuestions(scope, names) {
			wanted[scope+"|"+questionKey(question)] = true
		}
	}
	return wanted
}

func queryMissing(
	sockets []browseSocket,
	cache *recordCache,
	scope string,
	instances map[string]bool,
	now time.Time,
) error {
	if len(instances) == 0 {
		return nil
	}
	questions := cache.missingQuestions(scope, instances)
	var send []dns.Question
	for _, question := range questions {
		if !cache.canAskMissing(scope, questionKey(question), now) {
			continue
		}
		if err := cache.markMissing(scope, questionKey(question), now); err != nil {
			return err
		}
		send = append(send, question)
	}
	for _, socket := range sockets {
		if socket.scope != scope {
			continue
		}
		for _, question := range send {
			msg := dns.Msg{Question: []dns.Question{question}}
			data, err := msg.Pack()
			if err != nil {
				return fmt.Errorf("pack mDNS resolution query: %w", err)
			}
			if _, writeErr := socket.conn.WriteToUDP(data, socket.group); writeErr != nil {
				return fmt.Errorf("send mDNS resolution query on %s: %w", socket.scope, writeErr)
			}
		}
	}
	return nil
}

func (entry cachedRecord) refreshAt() time.Time {
	if entry.refreshStep >= refreshSteps || !entry.flushAt.IsZero() {
		return time.Time{}
	}
	ttl := time.Duration(entry.rr.Header().Ttl) * time.Second
	return entry.received.Add(ttl / 100 * time.Duration(refreshFirstPercent+entry.refreshStep*refreshStepPercent))
}

func (c *recordCache) nextMissingWake(now, wake time.Time) time.Time {
	wanted := c.wantedMissing()
	for key := range c.missingLast {
		if !wanted[key] {
			delete(c.missingLast, key)
		}
	}
	for key := range wanted {
		if _, exists := c.missingLast[key]; !exists {
			return now
		}
	}
	for _, retry := range c.missingLast {
		if retry.next.Before(wake) {
			wake = retry.next
		}
	}
	return wake
}

func (c *recordCache) nextWake(now, browse time.Time) time.Time {
	wake := browse
	interested := make(map[string]map[string]bool)
	for _, entry := range c.records {
		if _, ok := interested[entry.scope]; !ok {
			interested[entry.scope] = make(map[string]bool)
			for _, q := range c.questions(entry.scope, now) {
				interested[entry.scope][questionKey(q)] = true
			}
		}
	}
	if len(c.flushed) > 0 {
		return now
	}
	wake = c.nextMissingWake(now, wake)
	for _, entry := range c.records {
		if entry.expires.Before(wake) {
			wake = entry.expires
		}
		if !entry.flushAt.IsZero() && entry.flushAt.Before(wake) {
			wake = entry.flushAt
		}
		if !interested[entry.scope][recordQuestion(entry.rr)] {
			continue
		}
		if refresh := entry.refreshAt(); !refresh.IsZero() && refresh.Before(wake) {
			wake = refresh
		}
	}
	return wake
}

// queryPlan selects missing records at browse times and refreshes cached records
// only as their TTL approaches expiry. Known answers use their remaining TTL.
//
//nolint:gocognit // Selection considers missing records, refresh deadlines, and known answers together.
func (c *recordCache) queryPlan(scope string, now time.Time, browse bool) ([]dns.Msg, map[string]bool, error) {
	questions := c.questions(scope, now)
	wanted := c.wantedMissing()
	byQuestion := make(map[string][]cachedRecord)
	for _, entry := range c.records {
		if entry.scope == scope {
			byQuestion[recordQuestion(entry.rr)] = append(byQuestion[recordQuestion(entry.rr)], entry)
		}
	}
	selected := make(map[string]bool)
	var messages []dns.Msg
	for _, q := range questions {
		key := questionKey(q)
		entries := byQuestion[key]
		due := false
		for _, entry := range entries {
			at := entry.refreshAt()
			if !at.IsZero() && !at.After(now) {
				due = true
			}
		}
		missing := len(entries) == 0
		if missing && (q.Qtype == dns.TypeA || q.Qtype == dns.TypeAAAA) {
			other := dns.TypeA
			if q.Qtype == dns.TypeA {
				other = dns.TypeAAAA
			}
			missing = len(byQuestion[questionKey(dns.Question{Name: q.Name, Qtype: other})]) == 0
		}
		retry, retrying := c.missingLast[scope+"|"+key]
		retryDue := missing && wanted[scope+"|"+key] && (!retrying || !retry.next.After(now))
		if missing && q.Qtype != dns.TypePTR && !c.canAskMissing(scope, key, now) {
			missing = false
		}
		if !due && !retryDue && (!browse || (q.Qtype != dns.TypePTR && !missing)) {
			continue
		}
		selected[key] = true
		answers := make([]dns.RR, 0, len(entries))
		for _, entry := range entries {
			remaining := entry.expires.Sub(now)
			ttl := time.Duration(entry.rr.Header().Ttl) * time.Second
			if remaining < ttl/2 || remaining < time.Second {
				continue
			}
			answer := dns.Copy(entry.rr)
			answer.Header().Class &= 0x7fff
			//nolint:gosec // The remaining TTL cannot exceed the uint32 wire TTL.
			answer.Header().Ttl = uint32(remaining / time.Second)
			answers = append(answers, answer)
		}
		sort.Slice(answers, func(i, j int) bool { return answers[i].String() < answers[j].String() })
		parts, err := packQuestions(q, answers)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, parts...)
	}
	return messages, selected, nil
}

// packQuestions uses RFC 6762 multipacket known-answer suppression when needed.
func packQuestions(q dns.Question, answers []dns.RR) ([]dns.Msg, error) {
	msg := dns.Msg{Question: []dns.Question{q}, Compress: true}
	var parts []dns.Msg
	for _, answer := range answers {
		msg.Answer = append(msg.Answer, answer)
		data, err := msg.Pack()
		if err != nil {
			return nil, fmt.Errorf("pack mDNS known answer: %w", err)
		}
		if len(data) <= maxQueryBytes {
			continue
		}
		msg.Answer = msg.Answer[:len(msg.Answer)-1]
		msg.Truncated = true
		parts = append(parts, msg)
		msg = dns.Msg{Answer: []dns.RR{answer}, Compress: true}
	}
	parts = append(parts, msg)
	return parts, nil
}

func query(sockets []browseSocket, cache *recordCache, now time.Time, browse bool) error {
	plans := make(map[string][]dns.Msg)
	selected := make(map[string]map[string]bool)
	for _, socket := range sockets {
		if _, ok := plans[socket.scope]; !ok {
			messages, questions, err := cache.queryPlan(socket.scope, now, browse)
			if err != nil {
				return err
			}
			plans[socket.scope], selected[socket.scope] = messages, questions
		}
		for _, msg := range plans[socket.scope] {
			data, err := msg.Pack()
			if err != nil {
				return fmt.Errorf("pack mDNS query: %w", err)
			}
			if _, writeErr := socket.conn.WriteToUDP(data, socket.group); writeErr != nil {
				return fmt.Errorf("send mDNS query on %s: %w", socket.scope, writeErr)
			}
		}
	}
	return cache.advanceRefresh(selected, now)
}

func (c *recordCache) advanceRefresh(selected map[string]map[string]bool, now time.Time) error {
	answered := make(map[string]bool)
	for key, entry := range c.records {
		if selected[entry.scope][recordQuestion(entry.rr)] {
			answered[entry.scope+"|"+recordQuestion(entry.rr)] = true
			for at := entry.refreshAt(); !at.IsZero() && !at.After(now); at = entry.refreshAt() {
				entry.refreshStep++
			}
			c.records[key] = entry
		}
	}
	for scope, questions := range selected {
		for key := range questions {
			if answered[scope+"|"+key] || key == questionKey(dns.Question{Name: serviceName, Qtype: dns.TypePTR}) {
				continue
			}
			if err := c.markMissing(scope, key, now); err != nil {
				return err
			}
		}
	}
	return nil
}
