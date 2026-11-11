package sandbox

import (
	"bytes"
	"fmt"
	"sync"
)

// capBuffer keeps the first head bytes and the last tail bytes of a stream.
// Commands like `yes` must not be able to grow memory without a limit.
// stdout and stderr are copied concurrently, so the buffer is locked.
type capBuffer struct {
	mu      sync.Mutex
	head    []byte
	tail    []byte
	pos     int
	full    bool
	total   int
	headMax int
	tailMax int
}

func newCapBuffer(headMax, tailMax int) *capBuffer {
	if headMax < 0 {
		headMax = 0
	}
	if tailMax < 0 {
		tailMax = 0
	}
	return &capBuffer{headMax: headMax, tailMax: tailMax}
}

func (b *capBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.headMax > 0 && len(b.head) < b.headMax {
		take := b.headMax - len(b.head)
		if take > len(p) {
			take = len(p)
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if b.tailMax > 0 {
		for _, c := range p {
			if !b.full {
				b.tail = append(b.tail, c)
				if len(b.tail) == b.tailMax {
					b.full = true
					b.pos = 0
				}
				continue
			}
			b.tail[b.pos] = c
			b.pos = (b.pos + 1) % len(b.tail)
		}
	}
	b.total += n
	return n, nil
}

func (b *capBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total <= len(b.head) {
		return validUTF8(b.head)
	}
	tail := b.orderedTail()
	if b.total <= len(b.head)+len(tail) && b.total <= b.headMax+b.tailMax {
		return validUTF8(append(append([]byte{}, b.head...), tail...))
	}
	omitted := b.total - len(b.head) - len(tail)
	if omitted < 0 {
		omitted = 0
	}
	return validUTF8(b.head) +
		fmt.Sprintf("\n\n...[%d bytes truncated]...\n\n", omitted) +
		validUTF8(tail)
}

func (b *capBuffer) orderedTail() []byte {
	if !b.full || b.pos == 0 {
		return append([]byte{}, b.tail...)
	}
	out := make([]byte, 0, len(b.tail))
	out = append(out, b.tail[b.pos:]...)
	out = append(out, b.tail[:b.pos]...)
	return out
}

func validUTF8(b []byte) string {
	return string(bytes.ToValidUTF8(b, nil))
}
