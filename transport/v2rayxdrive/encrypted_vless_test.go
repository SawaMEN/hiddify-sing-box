package xdrive

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/vless/encryption"
	"github.com/sagernet/sing-vmess/vless"
	"github.com/sagernet/sing/common/auth"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type encryptedHandler struct {
	decryption *encryption.ServerInstance
	service    *vless.Service[string]
	errors     chan error
}

func (h *encryptedHandler) NewConnectionEx(ctx context.Context, c net.Conn, source, _ M.Socksaddr, close N.CloseHandlerFunc) {
	encrypted, err := h.decryption.Handshake(c, nil)
	if err == nil {
		err = h.service.NewConnection(ctx, encrypted, source, close)
	}
	if err != nil {
		h.errors <- err
		c.Close()
	}
}

type authenticatedEcho struct{ users chan string }

func (h authenticatedEcho) NewConnectionEx(ctx context.Context, c net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	defer c.Close()
	user, _ := auth.UserFromContext[string](ctx)
	h.users <- user
	io.Copy(c, c)
}
func (authenticatedEcho) NewPacketConnectionEx(_ context.Context, c N.PacketConn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	c.Close()
}

func TestNativeXDriveEncryptedVLESS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverEncryption := new(encryption.ServerInstance)
	if err = serverEncryption.Init([][]byte{key.Bytes()}, 0, 600, 0, ""); err != nil {
		t.Fatal(err)
	}
	defer serverEncryption.Close()
	clientEncryption := new(encryption.ClientInstance)
	if err = clientEncryption.Init([][]byte{key.PublicKey().Bytes()}, 0, 1, ""); err != nil {
		t.Fatal(err)
	}
	uuid := "12345678-1234-1234-1234-123456789012"
	users := make(chan string, 1)
	errors := make(chan error, 1)
	handler := &encryptedHandler{decryption: serverEncryption, service: vless.NewService[string](nil, authenticatedEcho{users: users}), errors: errors}
	handler.service.UpdateUsers([]string{"alice"}, []string{uuid}, []string{""})
	config := option.V2RayXDriveOptions{Service: "local", RemoteFolder: t.TempDir(), PollIntervalMs: 2, MaxPollIntervalMs: 5, FlushIntervalMs: 1}
	server, err := NewServer(ctx, nil, config, nil, handler)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	transport, err := NewClient(ctx, &testDialer{}, M.Socksaddr{}, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	stream, err := transport.DialContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(testPatience))
	encrypted, err := clientEncryption.Handshake(stream)
	if err != nil {
		t.Fatal(err)
	}
	client, err := vless.NewClient(uuid, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.DialConn(encrypted, M.ParseSocksaddr("destination.example:80"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = io.WriteString(conn, "encrypted native XDrive payload"); err != nil {
		t.Fatal(err)
	}
	expectRead(t, conn, "encrypted native XDrive payload")
	select {
	case user := <-users:
		if user != "alice" {
			t.Fatalf("authenticated user = %q", user)
		}
	case err := <-errors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
