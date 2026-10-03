// P2P CAS sharing over the local subnet: minimal mDNS (RFC 6762) discovery
// for _ebuild-cas._tcp.local plus TCP blob fetch using the digest model.
// No external dependencies; DNS packets are encoded/decoded by hand.
package daemon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	mdnsAddr       = "224.0.0.251:5353"
	serviceName    = "_ebuild-cas._tcp.local"
	defaultTCPPort = 44732
)

// Peer is a discovered CAS holder.
type Peer struct {
	Host string
	Port int
	Last time.Time
}

// encodeName writes a DNS name in label format.
func encodeName(buf *bytes.Buffer, name string) {
	for _, part := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		buf.WriteByte(byte(len(part)))
		buf.WriteString(part)
	}
	buf.WriteByte(0)
}

// Announce broadcasts our CAS service via mDNS response (PTR+SRV+A).
func Announce(port int, ttl time.Duration, stop <-chan struct{}) error {
	addr, err := net.ResolveUDPAddr("udp4", mdnsAddr)
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	host := hostName()
	ip := outboundIP()
	pkt := buildAnnounce(host, ip, port)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		_ = ttl
		if _, err := conn.Write(pkt); err != nil {
			return err
		}
		select {
		case <-stop:
			return nil
		case <-tick.C:
		}
	}
}

func hostName() string {
	if h, err := net.LookupCNAME("localhost"); err == nil && h != "" {
		return strings.TrimSuffix(h, ".") + ".local"
	}
	return "ebuild.local"
}

func outboundIP() net.IP {
	c, err := net.Dial("udp4", "224.0.0.251:5353")
	if err != nil {
		return net.ParseIP("127.0.0.1")
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP
	}
	return net.ParseIP("127.0.0.1")
}

func buildAnnounce(host string, ip net.IP, port int) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint16(0))      // id
	binary.Write(&b, binary.BigEndian, uint16(0x8400)) // response|authoritative
	binary.Write(&b, binary.BigEndian, uint16(0))      // qdcount
	binary.Write(&b, binary.BigEndian, uint16(3))      // ancount
	binary.Write(&b, binary.BigEndian, uint16(0))      // nscount
	binary.Write(&b, binary.BigEndian, uint16(0))      // arcount
	// PTR: service -> instance
	encodeName(&b, serviceName)
	binary.Write(&b, binary.BigEndian, uint16(12))
	binary.Write(&b, binary.BigEndian, uint16(1))
	binary.Write(&b, binary.BigEndian, uint32(120))
	var rdata bytes.Buffer
	encodeName(&rdata, host+"."+serviceName)
	binary.Write(&b, binary.BigEndian, uint16(rdata.Len()))
	b.Write(rdata.Bytes())
	// SRV: instance -> host:port
	encodeName(&b, host+"."+serviceName)
	binary.Write(&b, binary.BigEndian, uint16(33))
	binary.Write(&b, binary.BigEndian, uint16(1))
	binary.Write(&b, binary.BigEndian, uint32(120))
	var srv bytes.Buffer
	binary.Write(&srv, binary.BigEndian, uint16(0))
	binary.Write(&srv, binary.BigEndian, uint16(0))
	binary.Write(&srv, binary.BigEndian, uint16(port))
	encodeName(&srv, host)
	binary.Write(&b, binary.BigEndian, uint16(srv.Len()))
	b.Write(srv.Bytes())
	// A: host -> ip
	encodeName(&b, host)
	binary.Write(&b, binary.BigEndian, uint16(1))
	binary.Write(&b, binary.BigEndian, uint16(1))
	binary.Write(&b, binary.BigEndian, uint32(120))
	binary.Write(&b, binary.BigEndian, uint16(4))
	if v4 := ip.To4(); v4 != nil {
		b.Write(v4)
	} else {
		b.Write([]byte{127, 0, 0, 1})
	}
	return b.Bytes()
}

// Browse listens for mDNS announcements for up to timeout.
func Browse(timeout time.Duration) ([]Peer, error) {
	addr, err := net.ResolveUDPAddr("udp4", mdnsAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	// Emit one PTR query to solicit responses.
	var q bytes.Buffer
	binary.Write(&q, binary.BigEndian, uint16(0x1234))
	binary.Write(&q, binary.BigEndian, uint16(0))
	binary.Write(&q, binary.BigEndian, uint16(1))
	binary.Write(&q, binary.BigEndian, uint16(0))
	binary.Write(&q, binary.BigEndian, uint16(0))
	binary.Write(&q, binary.BigEndian, uint16(0))
	encodeName(&q, serviceName)
	binary.Write(&q, binary.BigEndian, uint16(12))
	binary.Write(&q, binary.BigEndian, uint16(1))
	_, _ = conn.WriteToUDP(q.Bytes(), addr)

	peers := map[string]Peer{}
	var mu sync.Mutex
	buf := make([]byte, 9000)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // timeout ends the browse window
		}
		for _, p := range parseAnnounce(buf[:n]) {
			mu.Lock()
			peers[p.Host] = p
			mu.Unlock()
		}
	}
	out := make([]Peer, 0, len(peers))
	for _, p := range peers {
		out = append(out, p)
	}
	return out, nil
}

// parseAnnounce extracts SRV+A pairs from an mDNS response (no compression
// pointers are emitted by Announce, so a linear reader suffices).
func parseAnnounce(pkt []byte) []Peer {
	if len(pkt) < 12 {
		return nil
	}
	ancount := int(binary.BigEndian.Uint16(pkt[6:8]))
	off := 12
	var readName func() string
	readName = func() string {
		var parts []string
		for off < len(pkt) && pkt[off] != 0 {
			// Compression pointer: follow it (RFC 6762 §18).
			if pkt[off]&0xC0 == 0xC0 {
				if off+1 >= len(pkt) {
					return ""
				}
				ptr := int(binary.BigEndian.Uint16(pkt[off:off+2]) & 0x3FFF)
				off += 2
				save := off
				off = ptr
				inner := readName()
				off = save
				parts = append(parts, inner)
				return strings.Join(parts, ".")
			}
			n := int(pkt[off])
			off++
			if off+n > len(pkt) {
				return ""
			}
			parts = append(parts, string(pkt[off:off+n]))
			off += n
		}
		off++ // NUL
		return strings.Join(parts, ".")
	}
	var hosts []string
	ports := map[string]int{}
	ips := map[string]string{}
	for i := 0; i < ancount && off+10 <= len(pkt); i++ {
		name := readName()
		if off+10 > len(pkt) {
			break
		}
		typ := binary.BigEndian.Uint16(pkt[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(pkt[off+8 : off+10]))
		off += 10
		if off+rdlen > len(pkt) {
			break
		}
		rdata := pkt[off : off+rdlen]
		off += rdlen
		switch typ {
		case 33: // SRV
			if len(rdata) > 6 {
				ports[name] = int(binary.BigEndian.Uint16(rdata[4:6]))
				// Target host follows; decode from rdata slice.
				sub := rdata[6:]
				var parts []string
				for len(sub) > 0 && sub[0] != 0 {
					if sub[0]&0xC0 == 0xC0 {
						break
					}
					n := int(sub[0])
					if len(sub) < 1+n {
						break
					}
					parts = append(parts, string(sub[1:1+n]))
					sub = sub[1+n:]
				}
				hosts = append(hosts, strings.Join(parts, "."))
			}
		case 1: // A
			if len(rdata) == 4 {
				ips[name] = fmt.Sprintf("%d.%d.%d.%d", rdata[0], rdata[1], rdata[2], rdata[3])
			}
		}
	}
	var peers []Peer
	for _, h := range hosts {
		ip := ips[h]
		if ip == "" {
			continue
		}
		port := ports[h]
		if port == 0 {
			port = defaultTCPPort
		}
		peers = append(peers, Peer{Host: ip, Port: port, Last: time.Now()})
	}
	return peers
}

// FetchBlob downloads hash from a peer over TCP (frame: 64-byte hex + 8-byte
// size request; raw bytes response) into w.
func FetchBlob(p Peer, hash string, w io.Writer) error {
	if len(hash) != 64 {
		return fmt.Errorf("ebuild: p2p: bad hash %q", hash)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", p.Host, p.Port), 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	var req [72]byte
	copy(req[:64], hash)
	if _, err := conn.Write(req[:]); err != nil {
		return err
	}
	_, err = io.Copy(w, conn)
	return err
}
