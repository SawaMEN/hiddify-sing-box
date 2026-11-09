package xhttp

// upload_queue reorders XHTTP packets without blocking Close behind a full
// upload channel or racing concurrent readers against the reassembly heap.
import (
	"container/heap"
	"errors"
	"io"
	"sync"

	E "github.com/sagernet/sing/common/exceptions"
)

type Packet struct {
	Reader  io.ReadCloser
	Payload []byte
	Seq     uint64
}

type uploadQueue struct {
	mu            sync.Mutex // guards closed, nomore and reader
	readMutex     sync.Mutex // serializes heap/reassembly and stream reads
	reader        io.ReadCloser
	nomore        bool
	pushedPackets chan Packet
	heap          uploadHeap
	nextSeq       uint64
	closed        bool
	done          chan struct{} // broadcast to blocked Push and Read
	space         chan struct{} // a receive frees one channel slot
	maxPackets    int
}

func NewUploadQueue(maxPackets int) *uploadQueue {
	if maxPackets < 1 {
		maxPackets = 1
	}
	return &uploadQueue{
		pushedPackets: make(chan Packet, maxPackets),
		maxPackets:    maxPackets,
		done:          make(chan struct{}),
		space:         make(chan struct{}, maxPackets),
	}
}

func (h *uploadQueue) Push(packet Packet) error {
	for {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return E.New("packet queue closed")
		}
		if h.nomore {
			h.mu.Unlock()
			return E.New("h.reader already exists")
		}
		// A nonblocking send while holding mu makes enqueue and Close atomic.
		select {
		case h.pushedPackets <- packet:
			if packet.Reader != nil {
				h.nomore = true
			}
			h.mu.Unlock()
			return nil
		default:
			h.mu.Unlock()
		}
		select {
		case <-h.done:
			return E.New("packet queue closed")
		case <-h.space:
		}
	}
}

func (h *uploadQueue) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	close(h.done)
	reader := h.reader
	var pending []io.ReadCloser
	// No Push can enqueue while mu is held. Drain and own every reader
	// rather than replacing an earlier stream without closing it.
drain:
	for {
		select {
		case packet := <-h.pushedPackets:
			if packet.Reader != nil {
				pending = append(pending, packet.Reader)
			}
		default:
			break drain
		}
	}
	h.mu.Unlock()

	var errs []error
	if reader != nil {
		errs = append(errs, reader.Close())
	}
	for _, r := range pending {
		errs = append(errs, r.Close())
	}
	return errors.Join(errs...)
}

func (h *uploadQueue) receive() (Packet, bool) {
	select {
	case <-h.done:
		return Packet{}, false
	case packet := <-h.pushedPackets:
		// Each receive notifies one producer; the signal buffer is bounded.
		select {
		case h.space <- struct{}{}:
		default:
		}
		return packet, true
	}
}

func (h *uploadQueue) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	h.readMutex.Lock()
	defer h.readMutex.Unlock()

	for {
		h.mu.Lock()
		closed, reader := h.closed, h.reader
		h.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		if reader != nil {
			return reader.Read(buffer)
		}
		if len(h.heap) > 0 {
			packet := h.heap[0]
			if packet.Seq < h.nextSeq {
				// A duplicated or delayed packet must not obstruct newer data.
				heap.Pop(&h.heap)
				continue
			}
			if packet.Seq == h.nextSeq {
				heap.Pop(&h.heap)
				n := copy(buffer, packet.Payload)
				if n < len(packet.Payload) {
					packet.Payload = packet.Payload[n:]
					heap.Push(&h.heap, packet)
				} else {
					h.nextSeq++
				}
				if n == 0 {
					continue
				}
				return n, nil
			}
			if len(h.heap) > h.maxPackets {
				return 0, E.New("packet queue is too large")
			}
		}
		packet, ok := h.receive()
		if !ok {
			return 0, io.EOF
		}
		if packet.Reader != nil {
			h.mu.Lock()
			if h.closed {
				h.mu.Unlock()
				_ = packet.Reader.Close()
				return 0, io.EOF
			}
			if len(h.heap) > 0 {
				h.mu.Unlock()
				_ = packet.Reader.Close()
				return 0, E.New("cannot mix stream and packet upload")
			}
			h.reader = packet.Reader
			h.mu.Unlock()
			return packet.Reader.Read(buffer)
		}
		heap.Push(&h.heap, packet)
	}
}

// heap code directly taken from https://pkg.go.dev/container/heap
type uploadHeap []Packet

func (h uploadHeap) Len() int           { return len(h) }
func (h uploadHeap) Less(i, j int) bool { return h[i].Seq < h[j].Seq }
func (h uploadHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *uploadHeap) Push(x any) {
	// Push and Pop use pointer receivers because they modify the slice's length,
	// not just its contents.
	*h = append(*h, x.(Packet))
}

func (h *uploadHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
