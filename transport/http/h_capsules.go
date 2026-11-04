package http

import (
 "io"
 "github.com/sagernet/sing/common"
 "github.com/sagernet/sing/common/buf"
)

func WriteDatagramCapsules(writer io.Writer, datagrams []*buf.Buffer) error {
	if len(datagrams) == 1 {
		return WriteDatagramCapsule(writer, datagrams[0])
	}
	defer buf.ReleaseMulti(datagrams)
	var totalLength int
	for _, datagram := range datagrams {
		totalLength += 1 + VarintLen(uint64(datagram.Len())) + datagram.Len()
	}
	capsules := buf.NewSize(min(totalLength, buf.MaxPooledBufferSize))
	defer capsules.Release()
	for _, datagram := range datagrams {
		length := uint64(datagram.Len())
		headerLength := 1 + VarintLen(length)
		if headerLength+datagram.Len() > capsules.FreeLen() {
			_, err := writer.Write(capsules.Bytes())
			if err != nil {
				return err
			}
			capsules.Reset()
		}
		header := capsules.Extend(headerLength)
		header[0] = CapsuleTypeDatagram
		PutVarint(header[1:], length)
		common.Must1(capsules.Write(datagram.Bytes()))
	}
	_, err := writer.Write(capsules.Bytes())
	return err
}
