package responses

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Real agent_message JSON from AxonHub #74455 (Codex parent → grok-4.5 subagent).
func TestConvertAgentMessageRawFrom74455(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
  "id": "amsg_019fae3e-76dd-7833-8122-7750e7016263",
  "type": "agent_message",
  "author": "/root",
  "content": [
    {
      "text": "Message Type: NEW_TASK\nTask name: /root/trellis_implement_grok_test\nSender: /root\nPayload:\n",
      "type": "input_text"
    },
    {
      "type": "encrypted_content",
      "encrypted_content": "gAAAAABqagvidgc7xIgh6gYf2wQ71fbXCFdhj9n71dL0NKxnT3MYjBvqRY5w14mvHjSnXUpyko-TCZNPp5gHykVEqECd3XydR_LA2LJ65jaLr3vbqwpVRo4voMIPmeSPpfcwnNSE44K9lMq9Ce-Uwo53c0oxIBSllS5bSSLKdsIiDSpEIu_YE9ziMFOaBh146FML-da9HaXK-oAPjDTUOOsvA76kkVEGIgQthcEyQnFDPAn8gzZZH9lh_xTM50I_b0epzaQZPZQFUIS2Qmd_iwmSKpGoy3RZlg=="
    }
  ],
  "recipient": "/root/trellis_implement_grok_test"
}`)
	out, ok := convertAgentMessageRawToUserMessage(raw)
	require.True(t, ok)
	var msg map[string]any
	require.NoError(t, json.Unmarshal(out, &msg))
	require.Equal(t, "message", msg["type"])
	require.Equal(t, "user", msg["role"])
	content, _ := json.Marshal(msg["content"])
	require.Contains(t, string(content), "NEW_TASK")
	require.Contains(t, string(content), "trellis_implement_grok_test")
	require.NotContains(t, string(out), "agent_message")
	require.NotContains(t, string(out), "encrypted_content")
	t.Logf("converted: %s", string(out))
}

func TestAgentMessageIsStructurallyRepresented(t *testing.T) {
	t.Parallel()
	require.True(t, isStructurallyRepresentedInputItem("agent_message"))
	require.True(t, isStructurallyRepresentedInputItem("message"))
	require.False(t, isStructurallyRepresentedInputItem("something_else"))
}
