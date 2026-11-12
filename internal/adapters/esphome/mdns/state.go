package mdns

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"time"

	"github.com/miekg/dns"

	"github.com/mholtzscher/hearth/internal/adapters/esphome"
)

// browserState owns all DNS-SD observations and scheduling for one browser run.
// A step expires the cache exactly once, applies its input, and returns effects.
type browserState struct {
	cache      *recordCache
	sent       map[esphome.MAC]resolvedRecord
	scopes     []string
	interval   time.Duration
	nextBrowse time.Time
	started    bool
}

type queryAction struct {
	scope      string
	message    dns.Msg
	resolution bool
}

type browserEffects struct {
	queries   []queryAction
	endpoints []esphome.DiscoveryRecord
	wake      time.Time
}

func newBrowserState(scopes []string, interval time.Duration, now time.Time) *browserState {
	return &browserState{
		cache:      newRecordCache(),
		sent:       make(map[esphome.MAC]resolvedRecord),
		scopes:     scopes,
		interval:   interval,
		nextBrowse: now,
	}
}

func (s *browserState) handlePacket(msg *dns.Msg, scope string, now time.Time) (browserEffects, error) {
	flushed := s.cache.expire(now)
	instances := s.cache.newInstances(msg, scope)
	if err := s.cache.ingest(msg, scope, now); err != nil {
		return browserEffects{}, err
	}
	var effects browserEffects
	for _, question := range s.cache.missingQuestions(scope, instances) {
		key := questionKey(question)
		if !s.cache.canAskMissing(scope, key, now) {
			continue
		}
		if err := s.cache.markMissing(scope, key, now); err != nil {
			return browserEffects{}, err
		}
		effects.queries = append(
			effects.queries,
			queryAction{scope: scope, message: dns.Msg{Question: []dns.Question{question}}, resolution: true},
		)
	}
	return s.finish(now, positiveRecords(msg, scope), flushed, effects)
}

func (s *browserState) tick(now time.Time) (browserEffects, error) {
	flushed := s.cache.expire(now)
	browse := !now.Before(s.nextBrowse)
	var effects browserEffects
	selected := make(map[string]map[string]bool)
	for _, scope := range s.scopes {
		messages, questions, err := s.cache.queryPlan(scope, now, browse)
		if err != nil {
			return browserEffects{}, err
		}
		selected[scope] = questions
		for _, msg := range messages {
			effects.queries = append(effects.queries, queryAction{scope: scope, message: msg})
		}
	}
	if err := s.cache.advanceRefresh(selected, now); err != nil {
		return browserEffects{}, err
	}
	if browse {
		if s.started {
			s.interval = nextBrowseInterval(s.interval)
		}
		s.started = true
		s.nextBrowse = now.Add(s.interval)
	}
	return s.finish(now, nil, flushed, effects)
}

func (s *browserState) finish(
	now time.Time,
	evidence, flushed map[string]bool,
	effects browserEffects,
) (browserEffects, error) {
	var err error
	effects.endpoints, err = s.endpoints(evidence, flushed)
	if err != nil {
		return browserEffects{}, err
	}
	s.cache.pruneMissing()
	effects.wake = s.cache.nextWake(now, s.nextBrowse)
	return effects, nil
}

func (s *browserState) endpoints(evidence, flushed map[string]bool) ([]esphome.DiscoveryRecord, error) {
	cache, sent := s.cache, s.sent
	var changes []esphome.DiscoveryRecord
	resolved := cache.resolve()
	candidates := make(map[esphome.MAC][]resolvedRecord)
	observed := make(map[esphome.MAC]map[string]bool)
	for _, candidate := range resolved {
		mac := candidate.record.MAC
		candidates[mac] = append(candidates[mac], candidate)
		if observed[mac] == nil {
			observed[mac] = make(map[string]bool)
		}
		for _, key := range candidate.addresses {
			observed[mac][key.key] = true
		}
	}
	historySize := 0
	for _, previous := range sent {
		historySize += len(previous.known)
	}
	keys := make([]esphome.MAC, 0, len(candidates))
	for mac := range candidates {
		keys = append(keys, mac)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, mac := range keys {
		previous, exists := sent[mac]
		r := nextEndpoint(candidates[mac], previous, exists, evidence, flushed)
		if !exists && len(sent) >= maxCacheRecords {
			return nil, fmt.Errorf("mDNS emitted endpoint limit (%d) exceeded", maxCacheRecords)
		}
		history, updatedSize, err := mergeKnown(previous.known, observed[mac], historySize)
		if err != nil {
			return nil, err
		}
		historySize = updatedSize
		r.known, r.addresses = history, nil
		if exists && previous.record == r.record {
			previous.known = history
			sent[mac] = previous
			continue
		}
		if r.record.Address == "" {
			continue
		}
		changes = append(changes, r.record)
		sent[mac] = r
	}
	return changes, nil
}

func mergeKnown(previous, observed map[string]bool, size int) (map[string]bool, int, error) {
	history := make(map[string]bool, len(previous)+len(observed))
	for key := range previous {
		history[key] = true
	}
	for key := range observed {
		if history[key] {
			continue
		}
		if size >= maxCacheRecords {
			return nil, size, fmt.Errorf("mDNS known address limit (%d) exceeded", maxCacheRecords)
		}
		size++
		history[key] = true
	}
	return history, size, nil
}

// nextEndpoint prefers the previously emitted source and address, then IPv4 and
// stable ordering. Expiry and known-alternate refreshes alone cannot change a
// published endpoint: changes require positive packet evidence or a confirmed
// same-family cache-flush replacement from that source.
//
//nolint:gocognit,gocyclo,cyclop,nestif // These choices jointly define one endpoint policy.
func nextEndpoint(
	candidates []resolvedRecord,
	previous resolvedRecord,
	exists bool,
	evidence, flushed map[string]bool,
) resolvedRecord {
	var chosen resolvedRecord
	have := false
	for _, candidate := range candidates {
		if exists && candidate.instance == previous.instance && candidate.target == previous.target {
			selected := candidate.addresses[0]
			foundOld := false
			for _, address := range candidate.addresses {
				if address.key == previous.addressKey {
					if address.addr.Is4() || !selected.addr.Is4() || !evidence[selected.key] ||
						previous.known[selected.key] {
						selected = address
					}
					foundOld = true
					break
				}
			}
			if !foundOld {
				for _, address := range candidate.addresses {
					if evidence[address.key] && !previous.known[address.key] {
						selected = address
						break
					}
				}
			}
			candidate.record.Address, candidate.addressKey = selected.addr.String(), selected.key
		}
		if exists && candidate.instance == previous.instance {
			chosen, have = candidate, true
			continue
		}
		if exists && have && chosen.instance == previous.instance {
			continue
		}
		ip := net.ParseIP(candidate.record.Address)
		oldIP := net.ParseIP(chosen.record.Address)
		is4, old4 := ip != nil && ip.To4() != nil, oldIP != nil && oldIP.To4() != nil
		if !have || is4 && !old4 || is4 == old4 &&
			(candidate.record.Address < chosen.record.Address || candidate.record.Address == chosen.record.Address &&
				(candidate.record.Port < chosen.record.Port || candidate.record.Port == chosen.record.Port && candidate.record.Encrypted && !chosen.record.Encrypted)) {
			chosen, have = candidate, true
		}
	}
	r := chosen
	if !exists {
		return r
	}
	oldAddr, oldErr := netip.ParseAddr(previous.record.Address)
	newAddr, newErr := netip.ParseAddr(r.record.Address)
	sameSource := r.instance == previous.instance && r.target == previous.target
	sameFamily := oldErr == nil && newErr == nil && oldAddr.Is4() == newAddr.Is4()
	flushReplacement := flushed[previous.addressKey] && sameSource && sameFamily
	newAddress := evidence[r.addressKey] && !previous.known[r.addressKey]
	if r.record.Address != previous.record.Address && !newAddress && !flushReplacement &&
		(r.target == previous.target || !evidence[r.srvKey]) {
		r.record.Address, r.addressKey = previous.record.Address, previous.addressKey
	}
	if r.record.Port != previous.record.Port && !evidence[r.srvKey] &&
		(r.target == previous.target || !newAddress) {
		r.record.Port = previous.record.Port
	}
	if r.record.Encrypted != previous.record.Encrypted && !evidence[r.txtKey] {
		r.record.Encrypted = previous.record.Encrypted
	}
	return r
}

func positiveRecords(msg *dns.Msg, scope string) map[string]bool {
	result := make(map[string]bool)
	if !msg.Response || msg.Truncated {
		return result
	}
	for _, records := range [][]dns.RR{msg.Answer, msg.Ns, msg.Extra} {
		for _, rr := range records {
			if rr.Header().Ttl > 0 && rr.Header().Class&0x7fff == dns.ClassINET && supported(rr) {
				result[recordKey(rr, scope)] = true
			}
		}
	}
	return result
}
