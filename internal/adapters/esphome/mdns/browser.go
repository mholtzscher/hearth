// Package mdns resolves ESPHome DNS-SD advertisements without authenticating them.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
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

//nolint:gocognit // Socket ownership, event processing and joining share one lifetime.
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

	scopes := make([]string, 0, len(sockets))
	seen := make(map[string]bool)
	for _, socket := range sockets {
		if !seen[socket.scope] {
			scopes = append(scopes, socket.scope)
			seen[socket.scope] = true
		}
	}
	now := time.Now()
	state := newBrowserState(scopes, interval, now)
	effects, err := state.tick(now)
	if err != nil {
		return err
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		if dispatchErr := dispatch(ctx, out, errs, sockets, effects); dispatchErr != nil {
			return dispatchErr
		}
		effects.queries, effects.endpoints = nil, nil
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(max(time.Until(effects.wake), 0))
		select {
		case <-ctx.Done():
			return nil
		case readErr := <-errs:
			return readErr
		case now = <-timer.C:
			effects, err = state.tick(now)
		case p := <-packets:
			var msg dns.Msg
			if unpackErr := msg.Unpack(p.data); unpackErr != nil {
				continue
			}
			effects, err = state.handlePacket(&msg, p.scope, time.Now())
		}
		if err != nil {
			return err
		}
	}
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

// dispatch applies a step's queries before publishing its endpoint changes.
//
//nolint:gocognit // Query and output failures retain their distinct safe context.
func dispatch(
	ctx context.Context,
	out chan<- esphome.DiscoveryRecord,
	errs <-chan error,
	sockets []browseSocket,
	effects browserEffects,
) error {
	for _, socket := range sockets {
		for _, action := range effects.queries {
			if socket.scope != action.scope {
				continue
			}
			data, err := action.message.Pack()
			if err != nil {
				if action.resolution {
					return fmt.Errorf("pack mDNS resolution query: %w", err)
				}
				return fmt.Errorf("pack mDNS query: %w", err)
			}
			if _, err = socket.conn.WriteToUDP(data, socket.group); err != nil {
				if action.resolution {
					return fmt.Errorf("send mDNS resolution query on %s: %w", socket.scope, err)
				}
				return fmt.Errorf("send mDNS query on %s: %w", socket.scope, err)
			}
		}
	}
	for _, record := range effects.endpoints {
		select {
		case out <- record:
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		}
	}
	return nil
}
