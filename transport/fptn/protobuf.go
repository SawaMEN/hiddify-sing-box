package fptn

import (
	"fmt"
	"google.golang.org/protobuf/encoding/protowire"
	"net/netip"
)

func bytesField(b []byte, n protowire.Number, data []byte) []byte {
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendBytes(b, data)
}
func message(kind uint64, field protowire.Number, data []byte) []byte {
	b := []byte{8, 1, 16}
	b = protowire.AppendVarint(b, kind)
	return bytesField(b, field, data)
}
func EncodePacket(packet []byte) []byte {
	inner := message(1, 4, bytesField(nil, 1, packet))
	batch := bytesField(nil, 1, inner)
	return message(3, 6, batch)
}
func fields(b []byte) (map[protowire.Number][][]byte, map[protowire.Number]uint64, error) {
	data := map[protowire.Number][][]byte{}
	nums := map[protowire.Number]uint64{}
	if len(b) > 256*1024 {
		return nil, nil, fmt.Errorf("fptn: oversized message")
	}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, nil, fmt.Errorf("fptn: malformed protobuf")
		}
		b = b[n:]
		switch typ {
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return nil, nil, fmt.Errorf("fptn: invalid protobuf length")
			}
			data[num] = append(data[num], v)
			b = b[n:]
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return nil, nil, fmt.Errorf("fptn: invalid protobuf integer")
			}
			nums[num] = v
			b = b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return nil, nil, fmt.Errorf("fptn: invalid protobuf field")
			}
			b = b[n:]
		}
	}
	return data, nums, nil
}
func one(m map[protowire.Number][][]byte, n protowire.Number) []byte {
	if len(m[n]) == 0 {
		return nil
	}
	return m[n][len(m[n])-1]
}
func DecodeAssignment(b []byte) ([]netip.Addr, error) {
	d, n, e := fields(b)
	if e != nil {
		return nil, e
	}
	if n[1] != 1 || n[2] != 2 {
		return nil, fmt.Errorf("fptn: IP assignment expected")
	}
	d, _, e = fields(one(d, 5))
	if e != nil {
		return nil, e
	}
	a4, e := netip.ParseAddr(string(one(d, 1)))
	if e != nil || !a4.Is4() {
		return nil, fmt.Errorf("fptn: invalid assigned IPv4")
	}
	a6, e := netip.ParseAddr(string(one(d, 2)))
	if e != nil || !a6.Is6() {
		return nil, fmt.Errorf("fptn: invalid assigned IPv6")
	}
	return []netip.Addr{a4, a6}, nil
}
func DecodePackets(b []byte) ([][]byte, error) {
	d, n, e := fields(b)
	if e != nil {
		return nil, e
	}
	if n[1] != 1 || n[2] != 3 {
		return nil, fmt.Errorf("fptn: invalid packet message")
	}
	batch, _, e := fields(one(d, 6))
	if e != nil {
		return nil, e
	}
	var packets [][]byte
	for _, inner := range batch[1] {
		d, n, e = fields(inner)
		if e != nil || n[1] != 1 || n[2] != 1 {
			return nil, fmt.Errorf("fptn: invalid inner packet")
		}
		p, _, e := fields(one(d, 4))
		if e != nil {
			return nil, e
		}
		data := one(p, 1)
		if len(data) < 20 || len(data) > 65535 {
			return nil, fmt.Errorf("fptn: invalid IP packet length")
		}
		packets = append(packets, data)
	}
	return packets, nil
}
