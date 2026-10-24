package snell

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func encodeQUICProxyPacket(psk, userKey []byte, destination M.Socksaddr, inner []byte) ([]byte,error) {
	host := destination.AddrString()
	if len(host)==0 || len(host)>255 || len(userKey)>255 || destination.Port==0 || !isQUICInitial(inner) {
		return nil, errors.New("snell QUIC: invalid initial, user key or destination")
	}
	plain := make([]byte,0,3+len(userKey)+1+len(host)+2+len(inner))
	plain=append(plain,1,1,byte(len(userKey)))
	plain=append(plain,userKey...)
	plain=append(plain,byte(len(host)))
	plain=append(plain,host...)
	plain=binary.BigEndian.AppendUint16(plain,destination.Port)
	plain=append(plain,inner...)
	if len(plain)>65535 { return nil, errors.New("snell QUIC: payload too large") }
	salt:=make([]byte,16)
	if _,err:=rand.Read(salt); err!=nil { return nil,err }
	aead,err:=quicProxyAEAD(psk,salt)
	if err!=nil{return nil,err}
	hdr:=make([]byte,7)
	hdr[0]=4
	binary.BigEndian.PutUint16(hdr[5:],uint16(len(plain)))
	nonce:=make([]byte,12)
	out:=append([]byte{},salt...)
	out=append(out,aead.Seal(nil,nonce,hdr,nil)...)
	binary.LittleEndian.PutUint64(nonce,1)
	return append(out,aead.Seal(nil,nonce,plain,nil)...),nil
}

// quicProxyLazyClient decides based on the actual first UDP datagram.
// Non-QUIC datagrams retain ordinary Snell UDP-over-TCP behavior.
type quicProxyLazyClient struct {
	ctx context.Context
	outbound *Outbound
	destination M.Socksaddr
	mu sync.Mutex
	ready chan struct{}
	conn net.PacketConn
	startupErr error
	closed bool
	readDeadline time.Time
	writeDeadline time.Time
}

func (c *quicProxyLazyClient) connect(initial []byte, addr net.Addr) error {
	if c.conn!=nil || c.startupErr!=nil {return c.startupErr}
	dest:=c.destination
	if !dest.IsValid() && addr!=nil { dest=M.SocksaddrFromNet(addr) }
	if dest.IsValid() && isQUICInitial(initial) {
		conn,err:=c.outbound.dialer.DialContext(c.ctx,N.NetworkUDP,c.outbound.serverAddr)
		if err!=nil {c.startupErr=err;close(c.ready);return err}
		c.conn=&quicProxyDatagramConn{Conn:conn,destination:dest,psk:c.outbound.quicPSK,userKey:c.outbound.quicUserKey}
	} else {
		conn,err:=c.outbound.dialer.DialContext(c.ctx,N.NetworkTCP,c.outbound.serverAddr)
		if err!=nil {c.startupErr=err;close(c.ready);return err}
		packetConn,err:=c.outbound.client.DialPacketConn(conn)
		if err!=nil {conn.Close();c.startupErr=err;close(c.ready);return err}
		c.conn=packetConn
	}
	if !c.readDeadline.IsZero() { _=c.conn.SetReadDeadline(c.readDeadline) }
	if !c.writeDeadline.IsZero() { _=c.conn.SetWriteDeadline(c.writeDeadline) }
	close(c.ready)
	return nil
}
func (c *quicProxyLazyClient) WriteTo(b []byte,addr net.Addr)(int,error){
	c.mu.Lock()
	if c.closed {c.mu.Unlock();return 0,net.ErrClosed}
	if err:=c.connect(b,addr);err!=nil{c.mu.Unlock();return 0,err}
	conn:=c.conn
	c.mu.Unlock()
	return conn.WriteTo(b,addr)
}
func (c *quicProxyLazyClient) ReadFrom(b []byte)(int,net.Addr,error){
	select{case <-c.ready:case <-c.ctx.Done():return 0,nil,c.ctx.Err()}
	c.mu.Lock(); conn,err:=c.conn,c.startupErr;c.mu.Unlock()
	if err!=nil{return 0,nil,err};if conn==nil{return 0,nil,net.ErrClosed}
	return conn.ReadFrom(b)
}
func(c *quicProxyLazyClient) Close()error{
	c.mu.Lock();defer c.mu.Unlock()
	if c.closed{return nil}
	c.closed=true
	if c.conn==nil && c.startupErr==nil {close(c.ready)}
	if c.conn!=nil{return c.conn.Close()}
	return nil
}
func(c *quicProxyLazyClient) LocalAddr()net.Addr{
	c.mu.Lock();defer c.mu.Unlock()
	if c.conn!=nil{return c.conn.LocalAddr()}
	return &net.UDPAddr{}
}
func(c *quicProxyLazyClient) SetDeadline(t time.Time)error{
	c.mu.Lock();defer c.mu.Unlock()
	c.readDeadline=t;c.writeDeadline=t
	if c.conn!=nil{return c.conn.SetDeadline(t)}
	return nil
}
func(c *quicProxyLazyClient) SetReadDeadline(t time.Time)error{
	c.mu.Lock();defer c.mu.Unlock()
	c.readDeadline=t
	if c.conn!=nil{return c.conn.SetReadDeadline(t)}
	return nil
}
func(c *quicProxyLazyClient) SetWriteDeadline(t time.Time)error{
	c.mu.Lock();defer c.mu.Unlock()
	c.writeDeadline=t
	if c.conn!=nil{return c.conn.SetWriteDeadline(t)}
	return nil
}

type quicProxyDatagramConn struct {
	net.Conn
	destination M.Socksaddr
	psk []byte
	userKey []byte
	mu sync.Mutex
	first bool
}
func(c *quicProxyDatagramConn) WriteTo(p []byte,_ net.Addr)(int,error){
	c.mu.Lock();defer c.mu.Unlock()
	if !c.first {
		wrapped,err:=encodeQUICProxyPacket(c.psk,c.userKey,c.destination,p)
		if err!=nil{return 0,err}
		_,err=c.Conn.Write(wrapped)
		if err!=nil{return 0,err}
		c.first=true
		return len(p),nil
	}
	return c.Conn.Write(p)
}
func(c *quicProxyDatagramConn) ReadFrom(p []byte)(int,net.Addr,error){
	n,err:=c.Conn.Read(p)
	return n,c.destination,err
}
