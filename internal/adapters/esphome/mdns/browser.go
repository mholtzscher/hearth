// Package mdns resolves ESPHome DNS-SD advertisements without authenticating them.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/mholtzscher/hearth/internal/adapters/esphome"
)

const browseInterval = time.Second
const ingressCapacity = 256
const maxPacketSize = 65535
const controlSize = 128
const udp6 = "udp6"
const multicastHopLimit = 255

// Browser browses on each active multicast-capable network interface.
type Browser struct{}

var _ esphome.DiscoverySource = Browser{}

type browseSocket struct {
	conn    *net.UDPConn
	group   *net.UDPAddr
	scope   string
	ifIndex int
	ipv6    bool
}

type packet struct {
	data  []byte
	scope string
}

// Run blocks until cancellation or a socket, cache, or ingress error. It never closes records.
func (Browser) Run(ctx context.Context, records chan<- esphome.DiscoveryRecord) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}
	sockets, err := openSockets()
	if err != nil {
		return err
	}
	return runSockets(ctx, records, sockets, browseInterval)
}

//nolint:gocognit // Each interface and address family requires independent socket setup.
func openSockets() ([]browseSocket, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list mDNS interfaces: %w", err)
	}
	var sockets []browseSocket
	var failures []error
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, addrErr := iface.Addrs()
		if addrErr != nil {
			failures = append(failures, addrErr)
			continue
		}
		var has4, has6 bool
		for _, addr := range addrs {
			ip, _, parseErr := net.ParseCIDR(addr.String())
			if parseErr != nil {
				continue
			}
			if ip.To4() != nil {
				has4 = true
			} else if ip.To16() != nil {
				has6 = true
			}
		}
		for _, family := range []struct {
			network, address string
			enabled          bool
		}{
			{"udp4", "224.0.0.251:5353", has4}, {udp6, "[ff02::fb]:5353", has6},
		} {
			if !family.enabled {
				continue
			}
			group, groupErr := net.ResolveUDPAddr(family.network, family.address)
			if groupErr != nil {
				failures = append(failures, groupErr)
				continue
			}
			if family.network == udp6 {
				group.Zone = iface.Name
			}
			conn, socketErr := net.ListenMulticastUDP(family.network, &iface, group)
			if socketErr != nil {
				failures = append(failures, fmt.Errorf("join mDNS on %s: %w", iface.Name, socketErr))
				continue
			}
			socketErr = configureSocket(conn, &iface, family.network)
			if socketErr != nil {
				_ = conn.Close()
				failures = append(failures, fmt.Errorf("select mDNS interface %s: %w", iface.Name, socketErr))
				continue
			}
			sockets = append(
				sockets,
				browseSocket{
					conn:    conn,
					group:   group,
					scope:   iface.Name,
					ifIndex: iface.Index,
					ipv6:    family.network == udp6,
				},
			)
		}
	}
	if len(sockets) == 0 {
		if len(failures) == 0 {
			return nil, errors.New("no usable mDNS multicast interfaces")
		}
		return nil, fmt.Errorf("no usable mDNS multicast sockets: %w", errors.Join(failures...))
	}
	return sockets, nil
}

func configureSocket(conn *net.UDPConn, iface *net.Interface, network string) error {
	if network == "udp4" {
		pc := ipv4.NewPacketConn(conn)
		if err := pc.SetMulticastInterface(iface); err != nil {
			return err
		}
		if err := pc.SetMulticastTTL(multicastHopLimit); err != nil {
			return err
		}
		return pc.SetControlMessage(ipv4.FlagInterface, true)
	}
	pc := ipv6.NewPacketConn(conn)
	if err := pc.SetMulticastInterface(iface); err != nil {
		return err
	}
	if err := pc.SetMulticastHopLimit(multicastHopLimit); err != nil {
		return err
	}
	return pc.SetControlMessage(ipv6.FlagInterface, true)
}

//nolint:gocognit,funlen // Socket ownership, event processing and joining share one lifetime.
func runSockets(
	ctx context.Context,
	out chan<- esphome.DiscoveryRecord,
	sockets []browseSocket,
	interval time.Duration,
) error {
	packets := make(chan packet, ingressCapacity)
	errs := make(chan error, len(sockets))
	var readers sync.WaitGroup
	for _, socket := range sockets {
		readers.Go(func() {
			buf := make([]byte, maxPacketSize)
			oob := make([]byte, controlSize)
			for {
				n, oobn, _, _, err := socket.conn.ReadMsgUDP(buf, oob)
				if err != nil {
					select {
					case errs <- fmt.Errorf("read mDNS: %w", err):
					default:
					}
					return
				}
				if !socket.onInterface(oob[:oobn]) {
					continue
				}
				p := packet{data: append([]byte(nil), buf[:n]...), scope: socket.scope}
				select {
				case packets <- p:
				case <-ctx.Done():
					return
				default:
					select {
					case errs <- errors.New("mDNS ingress capacity exceeded"):
					default:
					}
					return
				}
			}
		})
	}
	defer func() {
		for _, socket := range sockets {
			_ = socket.conn.Close()
		}
		readers.Wait()
	}()
	cache := newRecordCache()
	sent := make(map[esphome.MAC]resolvedRecord)
	now := time.Now()
	if err := query(sockets, cache, now, true); err != nil {
		return err
	}
	nextBrowse := now.Add(interval)
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		now = time.Now()
		wake := cache.nextWake(now, nextBrowse)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(max(time.Until(wake), 0))
		select {
		case <-ctx.Done():
			return nil
		case readErr := <-errs:
			return readErr
		case now = <-timer.C:
			browse := !now.Before(nextBrowse)
			if err := query(sockets, cache, now, browse); err != nil {
				return err
			}
			if browse {
				interval = nextBrowseInterval(interval)
				nextBrowse = now.Add(interval)
			}
			if err := emit(ctx, out, errs, sent, cache, now, nil); err != nil {
				return err
			}
		case p := <-packets:
			var msg dns.Msg
			if err := msg.Unpack(p.data); err != nil {
				continue
			}
			now = time.Now()
			cache.expire(now)
			instances := cache.newInstances(&msg, p.scope)
			if err := cache.ingest(&msg, p.scope, now); err != nil {
				return err
			}
			if err := queryMissing(sockets, cache, p.scope, instances, now); err != nil {
				return err
			}
			evidence := positiveRecords(&msg, p.scope)
			if err := emit(ctx, out, errs, sent, cache, now, evidence); err != nil {
				return err
			}
		}
	}
}

func emit(
	ctx context.Context,
	out chan<- esphome.DiscoveryRecord,
	errs <-chan error,
	sent map[esphome.MAC]resolvedRecord,
	cache *recordCache,
	now time.Time,
	evidence map[string]bool,
) error {
	resolved := cache.resolve(now, sent, evidence)
	current := preferred(resolved, sent)
	observed := make(map[esphome.MAC]map[string]bool)
	for _, candidate := range resolved {
		mac := candidate.record.MAC
		if observed[mac] == nil {
			observed[mac] = make(map[string]bool)
		}
		for _, key := range candidate.addresses {
			observed[mac][key] = true
		}
	}
	historySize := 0
	for _, previous := range sent {
		historySize += len(previous.known)
	}
	flushed := cache.flushed
	cache.flushed = make(map[string]bool)
	keys := make([]esphome.MAC, 0, len(current))
	for mac := range current {
		keys = append(keys, mac)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, mac := range keys {
		r := current[mac]
		previous, exists := sent[mac]
		if exists {
			r = evidencedUpdate(r, previous, evidence, flushed)
		} else if len(sent) >= maxCacheRecords {
			return fmt.Errorf("mDNS emitted endpoint limit (%d) exceeded", maxCacheRecords)
		}
		history, updatedSize, err := mergeKnown(previous.known, observed[mac], historySize)
		if err != nil {
			return err
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
		select {
		case out <- r.record:
			sent[mac] = r
		case <-ctx.Done():
			return nil
		case readErr := <-errs:
			return readErr
		}
	}
	return nil
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

func evidencedUpdate(r, previous resolvedRecord, evidence, flushed map[string]bool) resolvedRecord {
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

func (socket browseSocket) onInterface(oob []byte) bool {
	if socket.ifIndex == 0 {
		return true
	}
	if socket.ipv6 {
		var control ipv6.ControlMessage
		return control.Parse(oob) == nil && control.IfIndex == socket.ifIndex
	}
	var control ipv4.ControlMessage
	return control.Parse(oob) == nil && control.IfIndex == socket.ifIndex
}

func preferred(
	instances map[string]resolvedRecord,
	previous map[esphome.MAC]resolvedRecord,
) map[esphome.MAC]resolvedRecord {
	result := make(map[esphome.MAC]resolvedRecord)
	for _, r := range instances {
		mac := r.record.MAC
		old, exists := result[mac]
		if prior, ok := previous[mac]; ok && r.instance == prior.instance {
			result[mac] = r
			continue
		}
		if prior, ok := previous[mac]; ok && exists && old.instance == prior.instance {
			continue
		}
		ip := net.ParseIP(r.record.Address)
		oldIP := net.ParseIP(old.record.Address)
		is4 := ip != nil && ip.To4() != nil
		old4 := oldIP != nil && oldIP.To4() != nil
		if !exists || is4 && !old4 ||
			is4 == old4 &&
				(r.record.Address < old.record.Address || r.record.Address == old.record.Address && (r.record.Port < old.record.Port || r.record.Port == old.record.Port && r.record.Encrypted && !old.record.Encrypted)) {
			result[mac] = r
		}
	}
	return result
}
