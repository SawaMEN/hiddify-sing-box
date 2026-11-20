package v2raygrpclite

import (
	std_bufio "bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/baderror"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/varbin"
)

// kanged from: https://github.com/Qv2ray/gun-lite

var _ net.Conn = (*GunConn)(nil)

type GunConn struct {
	rawReader      io.Reader
	reader         *std_bufio.Reader
	writer         io.Writer
	flusher        http.Flusher
	create         chan struct{}
	err            error
	cancel         context.CancelFunc
	readRemaining  int
	frameRemaining int64
	multiMode      bool
	onClose        func()
}

func newGunConn(reader io.Reader, writer io.Writer, flusher http.Flusher) *GunConn {
	return &GunConn{
		rawReader: reader,
		reader:    std_bufio.NewReader(reader),
		writer:    writer,
		flusher:   flusher,
	}
}

func newLateGunConn(writer io.Writer, cancel context.CancelFunc) *GunConn {
	return &GunConn{
		create: make(chan struct{}),
		writer: writer,
		cancel: cancel,
	}
}

func (c *GunConn) setup(reader io.Reader, err error) {
	if reader != nil {
		c.rawReader = reader
		c.reader = std_bufio.NewReader(reader)
	}
	c.err = err
	close(c.create)
}

func (c *GunConn) Read(b []byte) (n int, err error) {
	n, err = c.read(b)
	return n, baderror.WrapH2(err)
}

func (c *GunConn) read(b []byte) (n int, err error) {
	if c.create != nil {
		<-c.create
		if c.err != nil {
			return 0, c.err
		}
	}

	if c.multiMode {
		return c.readMulti(b)
	}
	if c.readRemaining > 0 {
		if len(b) > c.readRemaining {
			b = b[:c.readRemaining]
		}
		n, err = c.reader.Read(b)
		c.readRemaining -= n
		return
	}

	_, err = c.reader.Discard(6)
	if err != nil {
		return
	}

	dataLen, err := binary.ReadUvarint(c.reader)
	if err != nil {
		return
	}

	c.readRemaining = int(dataLen)
	if len(b) > c.readRemaining {
		b = b[:c.readRemaining]
	}

	n, err = c.reader.Read(b)
	c.readRemaining -= n
	return
}

// readMulti decodes Xray TunMulti's MultiHunk: repeated protobuf "bytes data = 1"
// fields in a length-prefixed gRPC message. The existing outbound writer sends
// one field per message, valid for both Hunk and MultiHunk. Unlike normal Tun,
// a MultiHunk reader must consume all fields inside one message.
func (c *GunConn) readMulti(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for {
		if c.readRemaining > 0 {
			if len(b) > c.readRemaining {
				b = b[:c.readRemaining]
			}
			n, err := c.reader.Read(b)
			c.readRemaining -= n
			c.frameRemaining -= int64(n)
			return n, err
		}
		if c.frameRemaining == 0 {
			var header [5]byte
			if _, err := io.ReadFull(c.reader, header[:]); err != nil {
				return 0, err
			}
			if header[0] != 0 {
				return 0, errors.New("v2ray-grpc: compressed TunMulti frame is unsupported")
			}
			length := binary.BigEndian.Uint32(header[1:])
			if length > 16*1024*1024 {
				return 0, errors.New("v2ray-grpc: TunMulti frame exceeds 16 MiB")
			}
			c.frameRemaining = int64(length)
			if length == 0 {
				continue
			}
		}
		field, err := c.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		c.frameRemaining--
		if field != 0x0A {
			return 0, errors.New("v2ray-grpc: invalid TunMulti protobuf field")
		}
		var length uint64
		terminated := false
		for shift := uint(0); shift < 70; shift += 7 {
			if c.frameRemaining == 0 {
				return 0, io.ErrUnexpectedEOF
			}
			part, err := c.reader.ReadByte()
			if err != nil {
				return 0, err
			}
			c.frameRemaining--
			if shift == 63 && part > 1 {
				return 0, errors.New("v2ray-grpc: invalid TunMulti length")
			}
			length |= uint64(part&0x7f) << shift
			if part < 0x80 {
				terminated = true
				break
			}
		}
		if !terminated || length > uint64(c.frameRemaining) {
			return 0, errors.New("v2ray-grpc: malformed TunMulti length")
		}
		c.readRemaining = int(length)
	}
}

func (c *GunConn) Write(b []byte) (n int, err error) {
	varLen := varbin.UvarintLen(uint64(len(b)))
	buffer := buf.NewSize(6 + varLen + len(b))
	header := buffer.Extend(6 + varLen)
	header[0] = 0x00
	binary.BigEndian.PutUint32(header[1:5], uint32(1+varLen+len(b)))
	header[5] = 0x0A
	binary.PutUvarint(header[6:], uint64(len(b)))
	common.Must1(buffer.Write(b))
	_, err = c.writer.Write(buffer.Bytes())
	if err != nil {
		return 0, baderror.WrapH2(err)
	}
	if c.flusher != nil {
		c.flusher.Flush()
	}
	return len(b), nil
}

func (c *GunConn) WriteBuffer(buffer *buf.Buffer) error {
	defer buffer.Release()
	dataLen := buffer.Len()
	varLen := varbin.UvarintLen(uint64(dataLen))
	header := buffer.ExtendHeader(6 + varLen)
	header[0] = 0x00
	binary.BigEndian.PutUint32(header[1:5], uint32(1+varLen+dataLen))
	header[5] = 0x0A
	binary.PutUvarint(header[6:], uint64(dataLen))
	err := common.Error(c.writer.Write(buffer.Bytes()))
	if err != nil {
		return baderror.WrapH2(err)
	}
	if c.flusher != nil {
		c.flusher.Flush()
	}
	return nil
}

func (c *GunConn) FrontHeadroom() int {
	return 6 + binary.MaxVarintLen64
}

func (c *GunConn) Close() error {
	var reader io.Reader
	if c.create != nil {
		select {
		case <-c.create:
			reader = c.rawReader
		default:
		}
	} else {
		reader = c.rawReader
	}
	err := common.Close(reader, c.writer)
	if c.cancel != nil {
		c.cancel()
	}
	if c.onClose != nil {
		c.onClose()
	}
	return err
}

func (c *GunConn) LocalAddr() net.Addr {
	return M.Socksaddr{}
}

func (c *GunConn) RemoteAddr() net.Addr {
	return M.Socksaddr{}
}

func (c *GunConn) SetDeadline(t time.Time) error {
	return os.ErrInvalid
}

func (c *GunConn) SetReadDeadline(t time.Time) error {
	return os.ErrInvalid
}

func (c *GunConn) SetWriteDeadline(t time.Time) error {
	return os.ErrInvalid
}

func (c *GunConn) NeedAdditionalReadDeadline() bool {
	return true
}
