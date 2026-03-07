import { ref, computed } from 'vue'
import { apiFetch } from './useAuth.js'

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

  // Helper: get or create the trailing text segment in the streaming message
  function trailingTextSegment() {
    const segs = streamingMessage.value.segments
    if (segs.length === 0 || segs[segs.length - 1].type !== 'text') {
      segs.push({ type: 'text', content: '' })
    }
    return segs[segs.length - 1]
  }

  function handleEvent(event) {
    switch (event.type) {
      case 'agent_label':
        // Start streaming for this agent
        streamingMessage.value = {
          from: event.agent_id,
          segments: [], // ordered: {type:'text', content} | {type:'tool', title, output, loading}
        }
        state.value = 'streaming'
        break

      case 'token':
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          trailingTextSegment().content += event.token
        }
        break

      case 'tool_call_started':
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          // ACP may send multiple tool_call events for the same ID (title refinement).
          // If a segment with this ID already exists, just update its title.
          const existing = streamingMessage.value.segments.find(
            (s) => s.type === 'tool' && s.id === event.id,
          )
          if (existing) {
            existing.title = event.title
          } else {
            streamingMessage.value.segments.push({
              type: 'tool',
              id: event.id,
              title: event.title,
              output: null,
              loading: true,
            })
          }
        }
        break

      case 'tool_call_output':
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          const seg = streamingMessage.value.segments.find(
            (s) => s.type === 'tool' && s.id === event.id,
          )
          if (seg) {
            seg.output = event.output
          }
        }
        break

      case 'tool_call_result':
        if (streamingMessage.value && streamingMessage.value.from === event.agent_id) {
          const tc = streamingMessage.value.segments.find(
            (s) => s.type === 'tool' && s.id === event.id,
          )
          if (tc) {
            tc.title = event.title
            if (event.output) {
              tc.output = event.output
            }
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

        // If we were streaming for this agent, merge segments
        if (
          streamingMessage.value &&
          streamingMessage.value.from === event.message.from
        ) {
          if (streamingMessage.value.segments.length > 0) {
            msg.segments = streamingMessage.value.segments
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
      const resp = await apiFetch('/api/v1/messages')
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
