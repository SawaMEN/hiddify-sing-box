package xhttp

import (
	"errors"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type xuiTrackedUploadReader struct {
	io.Reader
	closeCount atomic.Int32
}

func (r *xuiTrackedUploadReader) Close() error {
	r.closeCount.Add(1)
	return nil
}

type xuiNotifyUploadReader struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (r *xuiNotifyUploadReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.ReadCloser.Read(p)
}

func TestXUIUploadQueueReorderPartialAndStale(t *testing.T) {
	q := NewUploadQueue(3)
	defer q.Close()
	for _, p := range []Packet{
		{Seq: 2, Payload: []byte("c")},
		{Seq: 0, Payload: []byte("ab")},
		{Seq: 1, Payload: []byte("B")},
	} {
		if err := q.Push(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"a", "b", "B", "c"} {
		buffer := make([]byte, 1)
		n, err := q.Read(buffer)
		if err != nil || n != 1 || string(buffer) != want {
			t.Fatalf("read: got %q (%d, %v), want %q", buffer[:n], n, err, want)
		}
	}
	if err := q.Push(Packet{Seq: 1, Payload: []byte("stale")}); err != nil {
		t.Fatal(err)
	}
	if err := q.Push(Packet{Seq: 3, Payload: []byte("d")}); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	n, err := q.Read(buffer)
	if err != nil || n != 1 || string(buffer) != "d" {
		t.Fatalf("stale packet blocked newest seq: %q, %d, %v", buffer[:n], n, err)
	}
}

func TestXUIUploadQueueCloseUnblocksFullProducer(t *testing.T) {
	q := NewUploadQueue(1)
	if err := q.Push(Packet{Seq: 0, Payload: []byte("a")}); err != nil {
		t.Fatal(err)
	}
	sent := make(chan error, 1)
	go func() { sent <- q.Push(Packet{Seq: 1, Payload: []byte("b")}) }()
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-sent:
		if err == nil {
			t.Fatal("producer was accepted after queue closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked producer did not wake after Close")
	}
	if err := q.Push(Packet{Payload: []byte("late")}); err == nil {
		t.Fatal("Push after Close succeeded")
	}
	if n, err := q.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("Read after Close: %d, %v", n, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestXUIUploadQueueCloseUnblocksStreamingReader(t *testing.T) {
	q := NewUploadQueue(1)
	pipeReader, pipeWriter := io.Pipe()
	defer pipeWriter.Close()
	reader := &xuiNotifyUploadReader{ReadCloser: pipeReader, started: make(chan struct{})}
	if err := q.Push(Packet{Reader: reader}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := q.Read(make([]byte, 1))
		result <- err
	}()
	select {
	case <-reader.started:
	case <-time.After(2 * time.Second):
		q.Close()
		t.Fatal("stream reader never began")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("stream remained readable after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close left streaming Read blocked")
	}
}

func TestXUIUploadQueueClosesUnconsumedStream(t *testing.T) {
	q := NewUploadQueue(1)
	reader := &xuiTrackedUploadReader{Reader: io.LimitReader(nil, 0)}
	if err := q.Push(Packet{Reader: reader}); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if got := reader.closeCount.Load(); got != 1 {
		t.Fatalf("queued stream closed %d times, want once", got)
	}
}

func TestXUIUploadQueueRejectsStreamMixedWithUnorderedPackets(t *testing.T) {
	q := NewUploadQueue(2)
	defer q.Close()
	reader := &xuiTrackedUploadReader{Reader: io.LimitReader(nil, 0)}
	if err := q.Push(Packet{Seq: 1, Payload: []byte("one")}); err != nil {
		t.Fatal(err)
	}
	if err := q.Push(Packet{Reader: reader}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Read(make([]byte, 1)); err == nil {
		t.Fatal("mixed stream and packets were silently accepted")
	}
	if got := reader.closeCount.Load(); got != 1 {
		t.Fatalf("rejected stream closed %d times", got)
	}
}

func TestXUIUploadQueueConcurrentReadersPreservePacketSequence(t *testing.T) {
	q := NewUploadQueue(4)
	defer q.Close()
	for seq, letter := range []byte("abcd") {
		if err := q.Push(Packet{Seq: uint64(seq), Payload: []byte{letter}}); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan byte, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var b [1]byte
			if n, err := q.Read(b[:]); err != nil || n != 1 {
				t.Errorf("concurrent read got %d bytes, error %v", n, err)
				return
			}
			results <- b[0]
		}()
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		q.Close()
		t.Fatal("concurrent readers became stuck")
	}
	close(results)
	var received []byte
	for letter := range results {
		received = append(received, letter)
	}
	sort.Slice(received, func(i, j int) bool { return received[i] < received[j] })
	if string(received) != "abcd" {
		t.Fatalf("duplicated or missing packets: %q", received)
	}
}
