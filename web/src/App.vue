<script setup>
import { ref, onMounted } from 'vue'
import { useSSE } from './composables/useSSE.js'
import { useChat } from './composables/useChat.js'
import { getToken, apiFetch } from './composables/useAuth.js'
import Header from './components/Header.vue'
import ChatPanel from './components/ChatPanel.vue'
import InputBar from './components/InputBar.vue'

const floorName = ref('')
const floorDescription = ref('')
const agents = ref([])

const { messages, streamingMessage, isStreaming, handleEvent, loadHistory } = useChat()
const sse = useSSE('/api/v1/events', getToken)

async function fetchMetadata() {
  try {
    const resp = await apiFetch('/api/v1/agents')
    const data = await resp.json()
    floorName.value = data.floor_name || 'OFC'
    floorDescription.value = data.description || ''
    agents.value = data.agents || []
  } catch {
    floorName.value = 'OFC'
  }
}

async function sendMessage(content) {
  await apiFetch('/api/v1/messages', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  })
}

onMounted(async () => {
  await fetchMetadata()
  await loadHistory()
  sse.onEvent(handleEvent)
  sse.connect()
})
</script>

<template>
  <div class="flex flex-col h-screen bg-slate-900">
    <Header :floorName="floorName" :description="floorDescription" :agents="agents" />
    <ChatPanel
      :messages="messages"
      :streamingMessage="streamingMessage"
      :agents="agents"
    />
    <InputBar :disabled="false" @send="sendMessage" />
  </div>
</template>
