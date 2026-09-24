//nolint:testpackage // These tests intentionally exercise unexported semantic internals.
package agentsemantic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type testWriteCloser struct {
	bytes.Buffer
}

func (*testWriteCloser) Close() error { return nil }

func TestBoundedBufferTruncatesWithoutShortWrite(t *testing.T) {
	t.Parallel()

	buffer := &boundedBuffer{max: 4}
	n, err := buffer.Write([]byte("abcdef"))
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.Equal(t, "abcd", buffer.String())

	n, err = buffer.Write([]byte("gh"))
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, "abcd", buffer.String())
}

func TestServerApplyEditIsRejected(t *testing.T) {
	t.Parallel()

	writer := &testWriteCloser{}
	client := &lspClient{
		stdin: writer,
		done:  make(chan struct{}),
	}
	client.handleServerRequest(json.RawMessage("1"), "workspace/applyEdit", json.RawMessage(`{"edit":{}}`))

	body, err := readLSPMessage(bufio.NewReader(bytes.NewReader(writer.Bytes())))
	require.NoError(t, err)

	var response struct {
		Result struct {
			Applied       bool   `json:"applied"`
			FailureReason string `json:"failureReason"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.False(t, response.Result.Applied)
	require.Equal(t, "Coder semantic tools are read-only", response.Result.FailureReason)
}

func TestReadLSPMessageRejectsOversizeFrameBeforeAllocation(t *testing.T) {
	t.Parallel()

	reader := bufio.NewReader(strings.NewReader(fmt.Sprintf(
		"Content-Length: %d\r\n\r\n",
		maxLSPMessageBytes+1,
	)))
	_, err := readLSPMessage(reader)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds internal safety limit")
}

func TestReadLSPMessageReadsBoundedFrame(t *testing.T) {
	t.Parallel()

	const body = "{\"jsonrpc\":\"2.0\"}"
	reader := bufio.NewReader(strings.NewReader(fmt.Sprintf(
		"Content-Length: %d\r\n\r\n%s",
		len(body),
		body,
	)))
	got, err := readLSPMessage(reader)
	require.NoError(t, err)
	require.Equal(t, body, string(got))
}
