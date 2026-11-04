package masque

import (
 "github.com/sagernet/sing-tun"
 "github.com/sagernet/sing/common/buf"
)

func (c *ClientEndpoint) WaitWriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	if !c.state.Load().configured {
		buf.ReleaseMulti(packetBuffers)
		return nil
	}
	err := c.device.WaitWriteInboundBuffers(packetBuffers)
	buf.ReleaseMulti(packetBuffers)
	return err
}

func (c *ClientEndpoint) FrontHeadroom() int {
	return c.device.FrontHeadroom()
}

func (s *ServerEndpoint) WaitWriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	err := s.device.WaitWriteInboundBuffers(packetBuffers)
	buf.ReleaseMulti(packetBuffers)
	return err
}

func (s *ServerEndpoint) FrontHeadroom() int {
	return s.device.FrontHeadroom()
}

func (s *ServerEndpoint) NewOutboundQueue(handler func(packetBuffers []*buf.Buffer)) *tun.OutboundQueue {
	return s.device.NewOutboundQueue(handler)
}

