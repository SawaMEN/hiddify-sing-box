package xdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

const testPatience = 10 * time.Second

type testDialer struct {
	count atomic.Int32
	fail  bool
}

func (d *testDialer) DialContext(ctx context.Context, network string, dest M.Socksaddr) (net.Conn, error) {
	d.count.Add(1)
	if d.fail {
		return nil, errors.New("native detour unavailable")
	}
	return (&net.Dialer{}).DialContext(ctx, network, dest.String())
}
func (d *testDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unused")
}

var _ N.Dialer = (*testDialer)(nil)

type echoHandler struct{}

func (echoHandler) NewConnectionEx(ctx context.Context, conn net.Conn, _, _ M.Socksaddr, onClose N.CloseHandlerFunc) {
	defer conn.Close()
	io.Copy(conn, conn)
	if onClose != nil {
		onClose(nil)
	}
}

func expectRead(t *testing.T, conn net.Conn, want string) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(testPatience))
	b := make([]byte, len(want))
	if _, err := io.ReadFull(conn, b); err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("got %q want %q", b, want)
	}
}

func TestNativeLocalXDriveRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := option.V2RayXDriveOptions{Service: "local", RemoteFolder: t.TempDir(), FlushIntervalMs: 1, PollIntervalMs: 2, MaxPollIntervalMs: 5}
	server := &Server{ctx: ctx, cancel: cancel, options: &streamOptions{ctx: ctx, ProtocolSettings: &config, dialer: &testDialer{}}, handler: echoHandler{}}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewClient(ctx, &testDialer{}, M.Socksaddr{}, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := client.DialContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := bytes.Repeat([]byte("native-xdrive-"), 100000)
	written := make(chan error, 1)
	go func() { _, err := conn.Write(payload); written <- err }()
	conn.SetReadDeadline(time.Now().Add(testPatience))
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, received); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, received) {
		t.Fatal("storage stream corrupted payload")
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if len(server.Network()) != 0 {
		t.Fatal("XDrive must not bind a TCP/UDP port")
	}
	client.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("closed transport still reads")
	}
}

func TestStorageUsesNativeDialerAndDoesNotBypassFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer server.Close()
	for _, fail := range []bool{false, true} {
		d := &testDialer{fail: fail}
		ctx, cancel := context.WithCancel(context.Background())
		s := &streamOptions{ctx: ctx, dialer: d, ProtocolSettings: &Config{}}
		client := newServiceClient(ctx, nil, s, time.Second, 2)
		response, err := client.Get(server.URL)
		if fail && err == nil {
			t.Fatal("failed native detour was bypassed")
		}
		if !fail && err != nil {
			t.Fatal(err)
		}
		if response != nil {
			response.Body.Close()
		}
		if d.count.Load() != 1 {
			t.Fatal("native dialer not used")
		}
		cancel()
		s.close()
	}
}

func TestTransportOptionsRoundTrip(t *testing.T) {
	// A copied option must retain credentials and tuning; the actual JSON codec
	// is tested by the option package's transport union.
	if err := validate(Config{Service: "google-drive", RemoteFolder: "folder", Secrets: []string{"id", "secret", "refresh"}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Config{{Service: "local"}, {Service: "google-drive", RemoteFolder: "folder"}, {Service: "local", RemoteFolder: "folder", Concurrency: 65}} {
		if validate(c) == nil {
			t.Fatalf("accepted invalid config %#v", c)
		}
	}
}

func TestNativeXDriveJSONCodec(t *testing.T) {
	input := option.V2RayTransportOptions{Type: "xdrive", XDriveOptions: option.V2RayXDriveOptions{Service: "google-drive", RemoteFolder: "folder", Secrets: []string{"id", "secret", "refresh"}, Concurrency: 8}}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output option.V2RayTransportOptions
	if err = json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if output.Type != "xdrive" || output.XDriveOptions.Concurrency != 8 || len(output.XDriveOptions.Secrets) != 3 {
		t.Fatalf("native options lost: %s", data)
	}
}

func TestDialContextCancellationKeepsEstablishedStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := option.V2RayXDriveOptions{Service: "local", RemoteFolder: t.TempDir(), FlushIntervalMs: 1, PollIntervalMs: 2, MaxPollIntervalMs: 5}
	server, err := NewServer(ctx, nil, config, nil, echoHandler{})
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewClient(ctx, &testDialer{}, M.Socksaddr{}, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dialCtx, endDial := context.WithCancel(ctx)
	conn, err := client.DialContext(dialCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	endDial()
	if _, err = conn.Write([]byte("after-dial-cancel")); err != nil {
		t.Fatal(err)
	}
	expectRead(t, conn, "after-dial-cancel")
}
