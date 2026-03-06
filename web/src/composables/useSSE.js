import { ref, onUnmounted } from 'vue'

/**
 * EventSource wrapper composable.
 * Connects to an SSE endpoint and emits parsed JSON events.
 */
export function useSSE(url) {
  const connected = ref(false)
  let eventSource = null
  let onEventCallback = null

  function connect() {
    if (eventSource) return

    eventSource = new EventSource(url)

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
