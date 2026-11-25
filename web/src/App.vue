<script setup>
import { ref, onMounted, provide } from 'vue'
import { useSSE } from './composables/useSSE.js'
import { useChat } from './composables/useChat.js'
import { useFurniture } from './composables/useFurniture.js'
import { getToken, apiFetch } from './composables/useAuth.js'
import { floorApi, setFloor, listFloors, apiError } from './composables/useFloor.js'
import Header from './components/Header.vue'
import ChatPanel from './components/ChatPanel.vue'
import InputBar from './components/InputBar.vue'
import Sidebar from './components/Sidebar.vue'
import SessionPicker from './components/SessionPicker.vue'
import FloorPicker from './components/FloorPicker.vue'
import BlueprintEditor from './components/BlueprintEditor.vue'

// ?floor=<id> names the floor. Without it, a server that manages floors
// (ofc building) shows the floor picker; one serving a single floor
// (ofc run) shows that floor.
const pickingFloor = ref(false)
// Whether the server manages floors: then the header links to the picker.
const manageable = ref(false)
// The ID of this tab's floor, once resolved.
const floorId = ref('')
// ?blueprint shows the floor's blueprint editor, on a server that manages
// floors.
const editingBlueprint = ref(false)
// ?sessions shows the session picker instead of a conversation.
const picking = new URLSearchParams(window.location.search).has('sessions')
const sessions = ref([])
// The signed-in user the server filtered the sessions for; '' if unknown.
const sessionsUser = ref('')

// urlWith returns the current URL with query params changed; a null
// value removes the param. Keeps the token.
function urlWith(changes) {
  const params = new URLSearchParams(window.location.search)
  for (const [key, value] of Object.entries(changes)) {
    if (value === null) {
      params.delete(key)
    } else {
      params.set(key, value)
    }
  }
  return `${window.location.pathname}?${params}`
}

const sessionsHref = urlWith({ sessions: '', session: null })
const sessionHref = (id) => urlWith({ sessions: null, session: id })
const floorsHref = urlWith({ floor: null, sessions: null, session: null, blueprint: null })
const floorHref = (id) => urlWith({ floor: id, sessions: null, session: null, blueprint: null })
const blueprintHref = (id) => urlWith({ floor: id, sessions: null, session: null, blueprint: '' })

async function fetchSessions() {
  const resp = await apiFetch(floorApi('/sessions'))
  if (resp.status === 401) {
    throw new Error('unauthorized')
  }
  const body = await resp.json()
  sessions.value = body.sessions || []
  sessionsUser.value = body.user || ''
}

const floorName = ref('')
const floorDescription = ref('')
const agents = ref([])
const furnitureRefreshKey = ref(0)
const sidebarOpen = ref(false)
const authError = ref('')

const { messages, streamingMessage, isStreaming, handleEvent, loadHistory } = useChat()
const { furniture, fetchFurniture, callTool } = useFurniture()
const sse = useSSE(getToken)

// API base of this tab's session, e.g. /api/v1/floors/<floor>/sessions/<id>
let sessionBase = ''

// resolveSession returns the session named by ?session=, or creates one
// and records it in the URL so a reload returns to the same session.
async function resolveSession() {
  const params = new URLSearchParams(window.location.search)
  let id = params.get('session')
  if (!id) {
    const resp = await apiFetch(floorApi('/sessions'), { method: 'POST' })
    if (resp.status === 401) {
      throw new Error('unauthorized')
    }
    id = (await resp.json()).id
    params.set('session', id)
    history.replaceState(null, '', `${window.location.pathname}?${params}`)
  }
  return id
}

async function fetchMetadata() {
  const resp = await apiFetch(floorApi('/agents'))
  if (resp.status === 401) {
    throw new Error('unauthorized')
  }
  if (!resp.ok) throw await apiError(resp)
  agents.value = (await resp.json()).agents || []
}

// resolveFloor picks this tab's floor (see pickingFloor) and returns
// false when the floor picker is to be shown instead.
async function resolveFloor() {
  const list = await listFloors()
  manageable.value = list.manageable
  let id = new URLSearchParams(window.location.search).get('floor')
  if (!id) {
    if (list.manageable || list.floors.length !== 1) {
      return false
    }
    id = list.floors[0].id
  }
  const entry = list.floors.find((f) => f.id === id)
  if (!entry) {
    throw new Error(`There is no floor "${id}" here.`)
  }
  floorName.value = entry.name
  floorDescription.value = entry.description || ''
  floorId.value = id
  setFloor(id)
  return true
}

async function sendMessage({ text, attachments }) {
  const resp = await apiFetch(`${sessionBase}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      content: text,
      attachments: attachments.map((a) => ({ id: a.id, name: a.name })),
    }),
  })
  if (!resp.ok) throw await apiError(resp)
}

// uploadImage stores an image in the session; a message then refers to
// the returned attachment by id.
async function uploadImage(file) {
  const name = encodeURIComponent(file.name || 'image')
  const resp = await apiFetch(`${sessionBase}/attachments?name=${name}`, {
    method: 'POST',
    headers: { 'Content-Type': file.type || 'application/octet-stream' },
    body: file,
  })
  if (!resp.ok) throw await apiError(resp)
  return resp.json()
}

// attachmentUrl is where an <img> loads a message's image from.
function attachmentUrl(attachment) {
  const token = tokenForUrls.value
  const query = token ? `?token=${encodeURIComponent(token)}` : ''
  return `${sessionBase}/attachments/${encodeURIComponent(attachment.id)}${query}`
}
const tokenForUrls = ref('')
getToken().then((t) => { tokenForUrls.value = t })
provide('attachmentUrl', attachmentUrl)

function handleEventWithRefresh(event) {
  // Sub-room events aren't shown yet.
  if (event.room_id) {
    return
  }
  handleEvent(event)

  // Server emits furniture_updated when any furniture state changes
  if (event.type === 'furniture_updated') {
    furnitureRefreshKey.value++
  }
}

onMounted(async () => {
  try {
    if (!(await resolveFloor())) {
      pickingFloor.value = true
      floorName.value = 'Floors'
      return
    }
    if (manageable.value && new URLSearchParams(window.location.search).has('blueprint')) {
      // The editor doesn't start the floor: a broken blueprint must stay
      // fixable.
      editingBlueprint.value = true
      return
    }
    await fetchMetadata()
    if (picking) {
      await fetchSessions()
      return
    }
    sessionBase = floorApi(`/sessions/${encodeURIComponent(await resolveSession())}`)
    const [lastSeq] = await Promise.all([loadHistory(`${sessionBase}/messages`), fetchFurniture()])
    sse.onEvent(handleEventWithRefresh)
    // Continue right after the loaded history; on reconnect the browser
    // sends Last-Event-ID itself.
    sse.connect(`${sessionBase}/events?last_event_id=${lastSeq}`)
  } catch (err) {
    if (err.message === 'unauthorized') {
      authError.value = 'Authentication required. Open the URL with token from the ofc console output.'
    } else if (err instanceof TypeError) {
      // fetch failed: no server answered
      authError.value = 'Cannot connect to ofc server. Is it running?'
    } else {
      // the server answered with an error, e.g. a floor that cannot start
      floorError.value = err.message
    }
  }
})
// floorError is the server's answer when this tab's floor can't be shown.
const floorError = ref('')
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
    <div v-else-if="floorError" class="flex items-center justify-center h-screen">
      <div class="text-center max-w-md px-6">
        <h2 class="text-xl font-semibold text-slate-200 mb-2">Cannot open this floor</h2>
        <p class="text-slate-400 whitespace-pre-wrap">{{ floorError }}</p>
        <p v-if="manageable" class="text-sm mt-4 space-x-4">
          <a :href="blueprintHref(floorId)" v-if="floorId" class="text-slate-400 hover:text-slate-200 underline">Edit its blueprint</a>
          <a :href="floorsHref" class="text-slate-400 hover:text-slate-200 underline">All floors</a>
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
      :sessionsHref="picking || pickingFloor || editingBlueprint ? '' : sessionsHref"
      :floorsHref="manageable && !pickingFloor ? floorsHref : ''"
      :blueprintHref="manageable && !pickingFloor && !editingBlueprint ? blueprintHref(floorId) : ''"
      @toggle-sidebar="sidebarOpen = !sidebarOpen"
    />
    <FloorPicker v-if="pickingFloor" :hrefFor="floorHref" :editHrefFor="blueprintHref" />
    <BlueprintEditor v-else-if="editingBlueprint" :floorId="floorId" :floorHref="floorHref(floorId)" />
    <SessionPicker v-else-if="picking" :sessions="sessions" :user="sessionsUser" :hrefFor="sessionHref" />
    <div v-else class="flex flex-1 min-h-0 relative">
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
        <InputBar :disabled="false" :send="sendMessage" :upload="uploadImage" />
      </div>
    </div>
    </template>
  </div>
</template>
