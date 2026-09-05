package agentproc

import (
	"fmt"
	"strings"
	"sync"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	// MaxHeadBytes is the number of bytes retained from the
	// beginning of the output for LLM consumption.
	MaxHeadBytes = 16 << 10 // 16KB

	// MaxTailBytes is the number of bytes retained from the
	// end of the output for LLM consumption.
	MaxTailBytes = 16 << 10 // 16KB

	// MaxStreamBytes is the bounded rolling window retained for incremental
	// cursor-based process output. It is separate from the legacy head+tail view.
	MaxStreamBytes = 64 << 10 // 64KB

	// DefaultIncrementalLimit bounds bytes returned by one cursor read.
	DefaultIncrementalLimit = 32 << 10 // 32KB

	// MaxLineLength is the maximum length of a single line
	// before it is truncated. This prevents minified files
	// or other long single-line output from consuming the
	// entire buffer.
	MaxLineLength = 2048

	// lineTruncationSuffix is appended to lines that exceed
	// MaxLineLength.
	lineTruncationSuffix = " ... [truncated]"
)

// HeadTailBuffer is a thread-safe buffer that captures process
// output and provides head+tail truncation for LLM consumption.
// It implements io.Writer so it can be used directly as
// cmd.Stdout or cmd.Stderr.
//
// The buffer stores up to MaxHeadBytes from the beginning of
// the output and up to MaxTailBytes from the end in a ring
// buffer, keeping total memory usage bounded regardless of
// how much output is written.
type HeadTailBuffer struct {
	mu         sync.Mutex
	cond       *sync.Cond
	head       []byte
	tail       []byte
	tailPos    int
	tailFull   bool
	stream     []byte
	streamPos  int
	streamFull bool
	headFull   bool
	closed     bool
	totalBytes int
	maxHead    int
	maxTail    int
	maxStream  int
}

// NewHeadTailBuffer creates a new HeadTailBuffer with the
// default head and tail sizes.
func NewHeadTailBuffer() *HeadTailBuffer {
	b := &HeadTailBuffer{
		maxHead:   MaxHeadBytes,
		maxTail:   MaxTailBytes,
		maxStream: MaxStreamBytes,
	}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// NewHeadTailBufferSized creates a HeadTailBuffer with custom
// head and tail sizes. This is useful for testing truncation
// logic with smaller buffers.
func NewHeadTailBufferSized(maxHead, maxTail int) *HeadTailBuffer {
	b := &HeadTailBuffer{
		maxHead:   maxHead,
		maxTail:   maxTail,
		maxStream: MaxStreamBytes,
	}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Write implements io.Writer. It is safe for concurrent use.
// All bytes are accepted; the return value always equals
// len(p) with a nil error.
func (b *HeadTailBuffer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	defer b.cond.Broadcast()

	n := len(p)
	b.totalBytes += n
	b.writeStream(p)

	// Fill head buffer if it is not yet full.
	if !b.headFull {
		remaining := b.maxHead - len(b.head)
		if remaining > 0 {
			take := remaining
			if take > len(p) {
				take = len(p)
			}
			b.head = append(b.head, p[:take]...)
			p = p[take:]
			if len(b.head) >= b.maxHead {
				b.headFull = true
			}
		}
		if len(p) == 0 {
			return n, nil
		}
	}

	// Write remaining bytes into the tail ring buffer.
	b.writeTail(p)
	return n, nil
}

// writeTail appends data to the tail ring buffer. The caller
// must hold b.mu.
func (b *HeadTailBuffer) writeStream(p []byte) {
	if b.maxStream <= 0 {
		return
	}
	if b.stream == nil {
		b.stream = make([]byte, b.maxStream)
	}
	for len(p) > 0 {
		space := b.maxStream - b.streamPos
		take := space
		if take > len(p) {
			take = len(p)
		}
		copy(b.stream[b.streamPos:b.streamPos+take], p[:take])
		p = p[take:]
		b.streamPos += take
		if b.streamPos >= b.maxStream {
			b.streamPos = 0
			b.streamFull = true
		}
	}
}

func (b *HeadTailBuffer) streamBytes() []byte {
	if b.stream == nil {
		return nil
	}
	if !b.streamFull {
		return b.stream[:b.streamPos]
	}
	out := make([]byte, b.maxStream)
	n := copy(out, b.stream[b.streamPos:])
	copy(out[n:], b.stream[:b.streamPos])
	return out
}

// ReadSince returns bounded incremental output from cursor. Cursor is an
// absolute byte position in the process output stream. If the caller fell
// behind the rolling window, gapBytes reports the number of evicted bytes.
func (b *HeadTailBuffer) ReadSince(cursor int64, limit int) (output string, nextCursor int64, gapBytes int64, hasMore bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if cursor < 0 {
		cursor = 0
	}
	if limit <= 0 || limit > DefaultIncrementalLimit {
		limit = DefaultIncrementalLimit
	}
	total := int64(b.totalBytes)
	stream := b.streamBytes()
	availableStart := total - int64(len(stream))
	if cursor < availableStart {
		gapBytes = availableStart - cursor
		cursor = availableStart
	}
	if cursor > total {
		cursor = total
	}
	start := int(cursor - availableStart)
	remaining := len(stream) - start
	if remaining < 0 {
		remaining = 0
	}
	take := remaining
	if take > limit {
		take = limit
	}
	if take > 0 {
		output = string(stream[start : start+take])
	}
	nextCursor = cursor + int64(take)
	hasMore = nextCursor < total
	return output, nextCursor, gapBytes, hasMore
}

func (b *HeadTailBuffer) writeTail(p []byte) {
	if b.maxTail <= 0 {
		return
	}

	// Lazily allocate the tail buffer on first use.
	if b.tail == nil {
		b.tail = make([]byte, b.maxTail)
	}

	for len(p) > 0 {
		// Write as many bytes as fit starting at tailPos.
		space := b.maxTail - b.tailPos
		take := space
		if take > len(p) {
			take = len(p)
		}
		copy(b.tail[b.tailPos:b.tailPos+take], p[:take])
		p = p[take:]
		b.tailPos += take
		if b.tailPos >= b.maxTail {
			b.tailPos = 0
			b.tailFull = true
		}
	}
}

// tailBytes returns the current tail contents in order. The
// caller must hold b.mu.
func (b *HeadTailBuffer) tailBytes() []byte {
	if b.tail == nil {
		return nil
	}
	if !b.tailFull {
		// Haven't wrapped yet; data is [0, tailPos).
		return b.tail[:b.tailPos]
	}
	// Wrapped: data is [tailPos, maxTail) + [0, tailPos).
	out := make([]byte, b.maxTail)
	n := copy(out, b.tail[b.tailPos:])
	copy(out[n:], b.tail[:b.tailPos])
	return out
}

// Bytes returns a copy of the raw buffer contents. If no
// truncation has occurred the full output is returned;
// otherwise the head and tail portions are concatenated.
func (b *HeadTailBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	tail := b.tailBytes()
	if len(tail) == 0 {
		out := make([]byte, len(b.head))
		copy(out, b.head)
		return out
	}
	out := make([]byte, len(b.head)+len(tail))
	copy(out, b.head)
	copy(out[len(b.head):], tail)
	return out
}

// Len returns the number of bytes currently stored in the
// buffer.
func (b *HeadTailBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	tailLen := 0
	if b.tailFull {
		tailLen = b.maxTail
	} else if b.tail != nil {
		tailLen = b.tailPos
	}
	return len(b.head) + tailLen
}

// TotalWritten returns the total number of bytes written to
// the buffer, which may exceed the stored capacity.
func (b *HeadTailBuffer) TotalWritten() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalBytes
}

// Output returns the truncated output suitable for LLM
// consumption, along with truncation metadata. If the total
// output fits within the head buffer alone, the full output is
// returned with nil truncation info. Otherwise the head and
// tail are joined with an omission marker and long lines are
// truncated.
func (b *HeadTailBuffer) Output() (string, *workspacesdk.ProcessTruncation) {
	b.mu.Lock()
	head := make([]byte, len(b.head))
	copy(head, b.head)
	tail := b.tailBytes()
	total := b.totalBytes
	headFull := b.headFull
	b.mu.Unlock()

	storedLen := len(head) + len(tail)

	// If everything fits, no head/tail split is needed.
	if !headFull || len(tail) == 0 {
		out := truncateLines(string(head))
		if total == 0 {
			return "", nil
		}
		return out, nil
	}

	// We have both head and tail data, meaning the total
	// output exceeded the head capacity. Build the
	// combined output with an omission marker.
	omitted := total - storedLen
	headStr := truncateLines(string(head))
	tailStr := truncateLines(string(tail))

	var sb strings.Builder
	_, _ = sb.WriteString(headStr)
	if omitted > 0 {
		_, _ = sb.WriteString(fmt.Sprintf(
			"\n\n... [omitted %d bytes] ...\n\n",
			omitted,
		))
	} else {
		// Head and tail are contiguous but were stored
		// separately because the head filled up.
		_, _ = sb.WriteString("\n")
	}
	_, _ = sb.WriteString(tailStr)
	result := sb.String()

	return result, &workspacesdk.ProcessTruncation{
		OriginalBytes: total,
		RetainedBytes: len(result),
		OmittedBytes:  omitted,
		Strategy:      "head_tail",
	}
}

// truncateLines scans the input line by line and truncates
// any line longer than MaxLineLength.
func truncateLines(s string) string {
	if len(s) <= MaxLineLength {
		// Fast path: if the entire string is shorter than
		// the max line length, no line can exceed it.
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	for len(s) > 0 {
		idx := strings.IndexByte(s, '\n')
		var line string
		if idx == -1 {
			line = s
			s = ""
		} else {
			line = s[:idx]
			s = s[idx+1:]
		}

		if len(line) > MaxLineLength {
			// Truncate preserving the suffix length so the
			// total does not exceed a reasonable size.
			cut := MaxLineLength - len(lineTruncationSuffix)
			if cut < 0 {
				cut = 0
			}
			_, _ = b.WriteString(line[:cut])
			_, _ = b.WriteString(lineTruncationSuffix)
		} else {
			_, _ = b.WriteString(line)
		}

		// Re-add the newline unless this was the final
		// segment without a trailing newline.
		if idx != -1 {
			_ = b.WriteByte('\n')
		}
	}

	return b.String()
}

// Close marks the buffer as closed and wakes any waiters.
// This is called when the process exits.
func (b *HeadTailBuffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.cond.Broadcast()
}

// Reset clears the buffer, discarding all data.
func (b *HeadTailBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = nil
	b.tail = nil
	b.tailPos = 0
	b.tailFull = false
	b.stream = nil
	b.streamPos = 0
	b.streamFull = false
	b.headFull = false
	b.closed = false
	b.totalBytes = 0
	b.cond.Broadcast()
}
