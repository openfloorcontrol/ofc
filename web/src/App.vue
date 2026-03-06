<script setup>
import { ref, onMounted } from 'vue'
import { useSSE } from './composables/useSSE.js'
import { useChat } from './composables/useChat.js'
import { useFurniture } from './composables/useFurniture.js'
import { getToken, apiFetch } from './composables/useAuth.js'
import Header from './components/Header.vue'
import ChatPanel from './components/ChatPanel.vue'
import InputBar from './components/InputBar.vue'
import Sidebar from './components/Sidebar.vue'

const floorName = ref('')
const floorDescription = ref('')
const agents = ref([])
const furnitureRefreshKey = ref(0)

const { messages, streamingMessage, isStreaming, handleEvent, loadHistory } = useChat()
const { furniture, fetchFurniture, callTool } = useFurniture()
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

function handleEventWithRefresh(event) {
  handleEvent(event)

  // Server emits furniture_updated when any furniture state changes
  if (event.type === 'furniture_updated') {
    furnitureRefreshKey.value++
  }
}

onMounted(async () => {
  await fetchMetadata()
  await Promise.all([loadHistory(), fetchFurniture()])
  sse.onEvent(handleEventWithRefresh)
  sse.connect()
})
</script>

<template>
  <div class="flex flex-col h-screen bg-slate-900">
    <Header :floorName="floorName" :description="floorDescription" :agents="agents" />
    <div class="flex flex-1 min-h-0">
      <Sidebar
        v-if="furniture.length > 0"
        :furniture="furniture"
        :callTool="callTool"
        :refreshKey="furnitureRefreshKey"
      />
      <div class="flex flex-col flex-1 min-w-0">
        <ChatPanel
          :messages="messages"
          :streamingMessage="streamingMessage"
          :agents="agents"
        />
        <InputBar :disabled="false" @send="sendMessage" />
      </div>
    </div>
  </div>
</template>
