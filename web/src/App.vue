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
const sidebarOpen = ref(false)
const authError = ref('')

const { messages, streamingMessage, isStreaming, handleEvent, loadHistory } = useChat()
const { furniture, fetchFurniture, callTool } = useFurniture()
const sse = useSSE('/api/v1/events', getToken)

async function fetchMetadata() {
  const resp = await apiFetch('/api/v1/agents')
  if (resp.status === 401) {
    throw new Error('unauthorized')
  }
  const data = await resp.json()
  floorName.value = data.floor_name || 'OFC'
  floorDescription.value = data.description || ''
  agents.value = data.agents || []
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
  try {
    await fetchMetadata()
    await Promise.all([loadHistory(), fetchFurniture()])
    sse.onEvent(handleEventWithRefresh)
    sse.connect()
  } catch (err) {
    if (err.message === 'unauthorized') {
      authError.value = 'Authentication required. Open the URL with token from the ofc console output.'
    } else {
      authError.value = 'Cannot connect to ofc server. Is it running?'
    }
  }
})
</script>

<template>
  <div class="flex flex-col h-screen bg-slate-900">
    <!-- Auth/connection error overlay -->
    <div v-if="authError" class="flex items-center justify-center h-screen">
      <div class="text-center max-w-md px-6">
        <div class="text-4xl mb-4">🔒</div>
        <h2 class="text-xl font-semibold text-slate-200 mb-2">Cannot connect</h2>
        <p class="text-slate-400">{{ authError }}</p>
        <p class="text-slate-500 text-sm mt-4">
          The URL should look like:<br>
          <code class="text-slate-400">http://127.0.0.1:8080?token=...</code>
        </p>
      </div>
    </div>
    <template v-else>
    <Header
      :floorName="floorName"
      :description="floorDescription"
      :agents="agents"
      :messages="messages"
      :hasFurniture="furniture.length > 0"
      :sidebarOpen="sidebarOpen"
      @toggle-sidebar="sidebarOpen = !sidebarOpen"
    />
    <div class="flex flex-1 min-h-0 relative">
      <!-- Mobile backdrop -->
      <div
        v-if="sidebarOpen && furniture.length > 0"
        class="fixed inset-0 z-20 bg-black/50 md:hidden"
        @click="sidebarOpen = false"
      />
      <Sidebar
        v-if="furniture.length > 0"
        :furniture="furniture"
        :callTool="callTool"
        :refreshKey="furnitureRefreshKey"
        :open="sidebarOpen"
        @close="sidebarOpen = false"
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
    </template>
  </div>
</template>
