import { ref, computed } from 'vue'

/**
 * Chat state machine composable.
 *
 * States:
 *   IDLE → agent_label → STREAMING
 *   STREAMING → token/tool_call_started/tool_call_result → STREAMING
 *   STREAMING → message_posted → IDLE
 *   IDLE → message_posted → IDLE
 *   IDLE → agent_passed/agent_error → IDLE
 */
export function useChat() {
  const messages = ref([])
  const streamingMessage = ref(null)
  const state = ref('idle') // 'idle' | 'streaming'

  function handleEvent(event) {
    switch (event.type) {
      case 'agent_label':
        // Start streaming for this agent
        streamingMessage.value = {
          from: event.agent_id,
          tokens: '',
          toolCalls: [],
        }
        state.value = 'streaming'
        break

      case 'token':
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          streamingMessage.value.tokens += event.token
        }
        break

      case 'tool_call_started':
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          streamingMessage.value.toolCalls.push({
            title: event.title,
            output: null,
            loading: true,
          })
        }
        break

      case 'tool_call_result':
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          // Find the matching tool call (last one with this title still loading)
          const tc = [...streamingMessage.value.toolCalls]
            .reverse()
            .find((t) => t.title === event.title && t.loading)
          if (tc) {
            tc.output = event.output
            tc.loading = false
          }
        }
        break

      case 'agent_finished':
        // Wait for message_posted to finalize
        break

      case 'message_posted': {
        const msg = {
          id: messages.value.length,
          from: event.message.from,
          content: event.message.content,
          toolInteractions: event.message.tool_interactions || [],
        }

        // If we were streaming for this agent, merge tool calls
        if (
          streamingMessage.value &&
          streamingMessage.value.from === event.message.from
        ) {
          if (streamingMessage.value.toolCalls.length > 0) {
            msg.toolCalls = streamingMessage.value.toolCalls
          }
          streamingMessage.value = null
          state.value = 'idle'
        }

        messages.value.push(msg)
        break
      }

      case 'agent_passed':
        messages.value.push({
          id: messages.value.length,
          from: event.agent_id,
          content: '[PASS]',
          isPass: true,
        })
        // Clear streaming if it was this agent
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          streamingMessage.value = null
          state.value = 'idle'
        }
        break

      case 'agent_error':
        messages.value.push({
          id: messages.value.length,
          from: event.agent_id,
          content: `Error: ${event.error}`,
          isError: true,
        })
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          streamingMessage.value = null
          state.value = 'idle'
        }
        break
    }
  }

  /**
   * Load initial messages from the REST API.
   */
  async function loadHistory() {
    try {
      const resp = await fetch('/api/v1/messages')
      const data = await resp.json()
      messages.value = (data.messages || []).map((m, i) => ({
        id: i,
        from: m.from,
        content: m.content,
        toolInteractions: m.tool_interactions || [],
      }))
    } catch {
      // silently ignore — SSE will catch up
    }
  }

  const isStreaming = computed(() => state.value === 'streaming')

  return {
    messages,
    streamingMessage,
    isStreaming,
    handleEvent,
    loadHistory,
  }
}
