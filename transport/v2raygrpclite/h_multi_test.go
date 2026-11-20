package v2raygrpclite

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func multiFrame(payload ...[]byte) []byte {
	var body []byte
	for _, chunk := range payload {
		body = append(body, 0x0A)
		body = binary.AppendUvarint(body, uint64(len(chunk)))
		body = append(body, chunk...)
	}
	result := make([]byte, 5)
	binary.BigEndian.PutUint32(result[1:], uint32(len(body)))
	return append(result, body...)
}

func TestH_TunMultiReadRepeatedFields(t *testing.T) {
	data := append(multiFrame([]byte("first"), nil, []byte("second")), multiFrame([]byte("third"))...)
	conn := newGunConn(bytes.NewReader(data), io.Discard, nil)
	conn.multiMode = true
	buf := make([]byte, len("firstsecondthird"))
	_, err := io.ReadFull(conn, buf)
	if err != nil || string(buf) != "firstsecondthird" {
		t.Fatalf("unexpected TunMulti stream %q: %v", buf, err)
	}
	if _, err = conn.Read(make([]byte, 2)); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestH_TunMultiReadRejectsMalformedFrames(t *testing.T) {
	for name, wire := range map[string][]byte{
		"length exceeds frame": {0, 0, 0, 0, 2, 0x0A, 0x03},
		"compressed":           {1, 0, 0, 0, 0},
		"oversized":            {0, 0x01, 0, 0, 1},
		"wrong field":          {0, 0, 0, 0, 1, 0x12},
	} {
		t.Run(name, func(t *testing.T) {
			conn := newGunConn(bytes.NewReader(wire), io.Discard, nil)
			conn.multiMode = true
			if _, err := conn.Read(make([]byte, 16)); err == nil {
				t.Fatal("malformed TunMulti accepted")
			}
		})
	}
}

func TestH_TunMultiWriteUsesCompatibleSingleRepeatedField(t *testing.T) {
	var output bytes.Buffer
	conn := newGunConn(bytes.NewReader(nil), &output, nil)
	conn.multiMode = true
	if _, err := conn.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), multiFrame([]byte("payload"))) {
		t.Fatalf("incorrect MultiHunk wire framing: %x", output.Bytes())
	}
}
