import { ref, onUnmounted } from 'vue'

/**
 * EventSource wrapper composable.
 * Connects to an SSE endpoint and emits parsed JSON events.
 * Accepts an optional token for authentication (passed as query param,
 * since EventSource cannot send custom headers).
 */
export function useSSE(url, tokenFn) {
  const connected = ref(false)
  let eventSource = null
  let onEventCallback = null

  async function connect() {
    if (eventSource) return

    let sseUrl = url
    if (tokenFn) {
      const token = await tokenFn()
      if (token) {
        const sep = url.includes('?') ? '&' : '?'
        sseUrl = `${url}${sep}token=${encodeURIComponent(token)}`
      }
    }

    eventSource = new EventSource(sseUrl)

    eventSource.onopen = () => {
      connected.value = true
    }

    eventSource.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        if (onEventCallback) {
          onEventCallback(data)
        }
      } catch {
        // ignore malformed events
      }
    }

    eventSource.onerror = () => {
      connected.value = false
    }
  }

  function disconnect() {
    if (eventSource) {
      eventSource.close()
      eventSource = null
      connected.value = false
    }
  }

  function onEvent(callback) {
    onEventCallback = callback
  }

  onUnmounted(disconnect)

  return { connected, connect, disconnect, onEvent }
}
