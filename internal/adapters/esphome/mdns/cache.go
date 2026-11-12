package mdns

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/mholtzscher/hearth/internal/adapters/esphome"
)

const serviceName = "_esphomelib._tcp.local."
const maxCacheRecords = 4096

type cachedRecord struct {
	rr          dns.RR
	scope       string
	expires     time.Time
	received    time.Time
	flushAt     time.Time
	serial      uint64
	refreshStep int
}

type recordCache struct {
	records     map[string]cachedRecord
	flushed     map[string]bool
	missingLast map[string]missingRetry
	serial      uint64
}

func newRecordCache() *recordCache {
	return &recordCache{
		records:     make(map[string]cachedRecord),
		flushed:     make(map[string]bool),
		missingLast: make(map[string]missingRetry),
	}
}

func name(s string) string { return strings.ToLower(dns.Fqdn(s)) }

func instanceName(s string) bool {
	labels := dns.SplitDomainName(name(s))
	return len(labels) == 4 && labels[0] != "" && labels[1] == "_esphomelib" && labels[2] == "_tcp" &&
		labels[3] == "local"
}

func recordKey(rr dns.RR, scope string) string {
	cloned := dns.Copy(rr)
	cloned.Header().Ttl = 0
	cloned.Header().Class &= 0x7fff
	cloned.Header().Name = name(cloned.Header().Name)
	return scope + "|" + cloned.String()
}

func (c *recordCache) expire(now time.Time) {
	for key, entry := range c.records {
		if !entry.expires.After(now) {
			if !entry.flushAt.IsZero() && !entry.flushAt.After(now) {
				c.flushed[key] = true
			}
			delete(c.records, key)
		}
	}
}

// ingest applies an entire packet together, so a cache-flush RRset with multiple
// addresses does not delete the other addresses from the same packet.
//
//nolint:gocognit // Cache flushes are applied as a packet-wide transaction before inserts.
func (c *recordCache) ingest(msg *dns.Msg, scope string, now time.Time) error {
	if !msg.Response || msg.Truncated {
		return nil
	}
	c.expire(now)
	rrs := make([]dns.RR, 0, len(msg.Answer)+len(msg.Ns)+len(msg.Extra))
	rrs = append(rrs, msg.Answer...)
	rrs = append(rrs, msg.Ns...)
	rrs = append(rrs, msg.Extra...)
	flush := make(map[string]bool)
	for _, rr := range rrs {
		h := rr.Header()
		if h.Class&0x7fff != dns.ClassINET || !supported(rr) {
			continue
		}
		if h.Class&0x8000 != 0 && h.Ttl > 0 {
			flush[fmt.Sprintf("%s|%s|%d", scope, name(h.Name), h.Rrtype)] = true
		}
	}
	for key, entry := range c.records {
		h := entry.rr.Header()
		if flush[fmt.Sprintf("%s|%s|%d", entry.scope, name(h.Name), h.Rrtype)] &&
			now.Sub(entry.received) >= time.Second {
			deadline := now.Add(time.Second)
			if entry.expires.After(deadline) {
				entry.expires = deadline
				entry.flushAt = deadline
				c.records[key] = entry
			}
		}
	}
	for _, rr := range rrs {
		h := rr.Header()
		if h.Class&0x7fff != dns.ClassINET || !supported(rr) {
			continue
		}
		key := recordKey(rr, scope)
		delete(c.missingLast, scope+"|"+recordQuestion(rr))
		if h.Ttl == 0 {
			delete(c.records, key)
			continue
		}
		if _, ok := c.records[key]; !ok && len(c.records) >= maxCacheRecords {
			return fmt.Errorf("mDNS record cache limit (%d) exceeded", maxCacheRecords)
		}
		c.serial++
		c.records[key] = cachedRecord{
			rr:       dns.Copy(rr),
			scope:    scope,
			expires:  now.Add(time.Duration(h.Ttl) * time.Second),
			received: now,
			serial:   c.serial,
		}
	}
	return nil
}

func supported(rr dns.RR) bool {
	switch r := rr.(type) {
	case *dns.PTR:
		return name(r.Hdr.Name) == serviceName && instanceName(r.Ptr)
	case *dns.SRV:
		return instanceName(r.Hdr.Name)
	case *dns.TXT:
		return instanceName(r.Hdr.Name)
	case *dns.A, *dns.AAAA:
		return strings.HasSuffix(name(rr.Header().Name), ".local.")
	default:
		return false
	}
}

func parseTXT(txt []string) (esphome.MAC, bool, bool) {
	var mac esphome.MAC
	var haveMAC, encrypted bool
	for _, item := range txt {
		key, value, _ := strings.Cut(item, "=")
		switch strings.ToLower(key) {
		case "mac":
			if haveMAC {
				return mac, false, false
			}
			var err error
			mac, err = esphome.ParseMAC(value)
			if err != nil {
				return mac, false, false
			}
			haveMAC = true
		case "api_encryption":
			encrypted = true
		}
	}
	return mac, encrypted, haveMAC
}

type resolvedRecord struct {
	record     esphome.DiscoveryRecord
	instance   string
	addressKey string
	srvKey     string
	txtKey     string
	target     string
	addresses  []string
	known      map[string]bool
}

type addressRecord struct {
	addr netip.Addr
	key  string
}

func chooseAddress(addresses []addressRecord, old resolvedRecord, evidence map[string]bool) addressRecord {
	chosen := addresses[0]
	for _, address := range addresses {
		if address.key == old.addressKey {
			if address.addr.Is4() || !chosen.addr.Is4() || !evidence[chosen.key] || old.known[chosen.key] {
				return address
			}
			return chosen
		}
	}
	for _, address := range addresses {
		if evidence[address.key] && !old.known[address.key] {
			return address
		}
	}
	return chosen
}

func (c *recordCache) resolved(now time.Time) map[string]esphome.DiscoveryRecord {
	result := make(map[string]esphome.DiscoveryRecord)
	for key, entry := range c.resolve(now, nil, nil) {
		result[key] = entry.record
	}
	return result
}

// resolve retains the selected address while it remains in the RRset.
//
//nolint:gocognit,gocyclo,cyclop,funlen // Resolution checks association and each required RRset together.
func (c *recordCache) resolve(
	now time.Time,
	previous map[esphome.MAC]resolvedRecord,
	evidence map[string]bool,
) map[string]resolvedRecord {
	c.expire(now)
	result := make(map[string]resolvedRecord)
	for _, ptrEntry := range c.records {
		ptr, ok := ptrEntry.rr.(*dns.PTR)
		if !ok {
			continue
		}
		instance := name(ptr.Ptr)
		var srv *dns.SRV
		var txt *dns.TXT
		var srvSerial, txtSerial uint64
		var srvKey, txtKey string
		for key, entry := range c.records {
			if entry.scope != ptrEntry.scope || name(entry.rr.Header().Name) != instance {
				continue
			}
			switch rr := entry.rr.(type) {
			case *dns.SRV:
				if entry.serial > srvSerial {
					srv, srvKey, srvSerial = rr, key, entry.serial
				}
			case *dns.TXT:
				if entry.serial > txtSerial {
					txt, txtKey, txtSerial = rr, key, entry.serial
				}
			}
		}
		if srv == nil || txt == nil || srv.Port == 0 || !strings.HasSuffix(name(srv.Target), ".local.") ||
			name(srv.Target) == ".local." {
			continue
		}
		{
			mac, encrypted, valid := parseTXT(txt.Txt)
			if !valid {
				continue
			}
			var addresses []addressRecord
			for key, entry := range c.records {
				if entry.scope != ptrEntry.scope || name(entry.rr.Header().Name) != name(srv.Target) {
					continue
				}
				var addr netip.Addr
				switch rr := entry.rr.(type) {
				case *dns.A:
					addr, _ = netip.AddrFromSlice(rr.A)
				case *dns.AAAA:
					addr, _ = netip.AddrFromSlice(rr.AAAA)
				}
				addr = addr.Unmap()
				if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() {
					continue
				}
				if addr.Is6() && addr.IsLinkLocalUnicast() {
					if entry.scope == "" {
						continue
					}
					addr = addr.WithZone(entry.scope)
				}
				addresses = append(addresses, addressRecord{addr: addr, key: key})
			}
			if len(addresses) == 0 {
				continue
			}
			sort.Slice(addresses, func(i, j int) bool {
				if addresses[i].addr.Is4() != addresses[j].addr.Is4() {
					return addresses[i].addr.Is4()
				}
				return addresses[i].addr.Compare(addresses[j].addr) < 0
			})
			chosen := addresses[0]
			instanceKey := ptrEntry.scope + "|" + instance
			if old, exists := previous[mac]; exists && old.instance == instanceKey && old.target == name(srv.Target) {
				chosen = chooseAddress(addresses, old, evidence)
			}
			keys := make([]string, 0, len(addresses))
			for _, address := range addresses {
				keys = append(keys, address.key)
			}
			result[instanceKey] = resolvedRecord{
				record: esphome.DiscoveryRecord{
					MAC:       mac,
					Address:   chosen.addr.String(),
					Port:      srv.Port,
					Encrypted: encrypted,
				},
				instance:   instanceKey,
				addressKey: chosen.key,
				srvKey:     srvKey,
				txtKey:     txtKey,
				target:     name(srv.Target),
				addresses:  keys,
			}
		}
	}
	return result
}

// questions asks for missing or refreshed components as well as browsing PTRs.
func (c *recordCache) questions(scope string, now time.Time) []dns.Question {
	c.expire(now)
	questions := []dns.Question{{Name: serviceName, Qtype: dns.TypePTR, Qclass: dns.ClassINET}}
	seen := map[string]bool{}
	appendQuestion := func(host string, typ uint16) {
		key := fmt.Sprintf("%s|%d", host, typ)
		if !seen[key] {
			questions = append(questions, dns.Question{Name: host, Qtype: typ, Qclass: dns.ClassINET})
			seen[key] = true
		}
	}
	for _, entry := range c.records {
		if entry.scope != scope {
			continue
		}
		var host string
		switch rr := entry.rr.(type) {
		case *dns.PTR:
			host = name(rr.Ptr)
			for _, typ := range []uint16{dns.TypeSRV, dns.TypeTXT} {
				appendQuestion(host, typ)
			}
		case *dns.SRV:
			if rr.Port == 0 || !strings.HasSuffix(name(rr.Target), ".local.") {
				continue
			}
			host = name(rr.Target)
			for _, typ := range []uint16{dns.TypeA, dns.TypeAAAA} {
				appendQuestion(host, typ)
			}
		}
	}
	return questions
}
