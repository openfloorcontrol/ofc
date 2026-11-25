<script setup>
import { ref, onMounted } from 'vue'
import { apiFetch } from '../composables/useAuth.js'
import { listFloors, apiError } from '../composables/useFloor.js'

const props = defineProps({
  // hrefFor(id) is the URL that opens floor id.
  hrefFor: { type: Function, required: true },
  // editHrefFor(id) is the URL of floor id's blueprint editor.
  editHrefFor: { type: Function, required: true },
})

const floors = ref([])
const manageable = ref(false)
const templates = ref([])
const template = ref('')
const busy = ref('') // id of the floor being stopped or deleted, or 'add'
const confirmDelete = ref('') // id of the floor whose delete awaits a second click
const renaming = ref('') // id of the floor whose name is being edited
const newName = ref('')
const error = ref('')

async function load() {
  const list = await listFloors()
  floors.value = list.floors
  manageable.value = list.manageable
  if (list.manageable && templates.value.length === 0) {
    const resp = await apiFetch('/api/v1/templates')
    templates.value = (await resp.json()).templates || []
    template.value = templates.value[0]?.name || ''
  }
}

// run does one change to the floors, then reloads the list. Errors show
// above the list.
async function run(id, request) {
  error.value = ''
  busy.value = id
  try {
    const resp = await request()
    if (!resp.ok) throw await apiError(resp)
    return resp
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = ''
    confirmDelete.value = ''
    await load()
  }
}

async function addFloor() {
  const resp = await run('add', () => apiFetch('/api/v1/floors', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ template: template.value }),
  }))
  if (resp) {
    window.location.href = props.hrefFor((await resp.json()).id)
  }
}

function startRename(f) {
  renaming.value = f.id
  newName.value = f.name
}

async function saveRename(id) {
  const name = newName.value.trim()
  renaming.value = ''
  if (!name) return
  await run(id, () => apiFetch(`/api/v1/floors/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  }))
}

function stopFloor(id) {
  run(id, () => apiFetch(`/api/v1/floors/${encodeURIComponent(id)}/stop`, { method: 'POST' }))
}

function deleteFloor(id) {
  if (confirmDelete.value !== id) {
    confirmDelete.value = id
    return
  }
  run(id, () => apiFetch(`/api/v1/floors/${encodeURIComponent(id)}`, { method: 'DELETE' }))
}

onMounted(() => load().catch((err) => { error.value = err.message }))
</script>

<template>
  <div class="flex-1 overflow-y-auto px-4 py-6">
    <div class="max-w-2xl mx-auto">
      <div class="flex items-center justify-between gap-3 mb-4">
        <h2 class="text-lg font-semibold text-slate-200">Floors</h2>
        <form v-if="manageable && templates.length > 0" class="flex items-center gap-2" @submit.prevent="addFloor">
          <select
            v-model="template"
            class="bg-slate-800 border border-slate-700 rounded px-2 py-1.5 text-sm text-slate-200"
            title="Template"
          >
            <option v-for="t in templates" :key="t.name" :value="t.name" :title="t.description">{{ t.name }}</option>
          </select>
          <button
            type="submit"
            :disabled="busy !== ''"
            class="px-3 py-1.5 rounded bg-slate-700 hover:bg-slate-600 disabled:opacity-50 text-sm text-slate-100 transition-colors"
          >{{ busy === 'add' ? 'Adding…' : 'Add floor' }}</button>
        </form>
      </div>
      <p v-if="error" class="text-red-400 text-sm mb-3">{{ error }}</p>
      <p v-if="manageable && templates.length === 0" class="text-slate-500 text-sm mb-3">
        No templates: add blueprints under <code class="text-slate-400">templates/&lt;name&gt;/blueprint.yaml</code> in the building directory.
      </p>
      <p v-if="floors.length === 0" class="text-slate-500 text-sm">No floors yet.</p>
      <ul v-else class="divide-y divide-slate-800 border border-slate-800 rounded">
        <li v-for="f in floors" :key="f.id" class="flex items-center gap-3 px-4 py-3 hover:bg-slate-800/60 transition-colors">
          <span
            class="w-2 h-2 rounded-full flex-shrink-0"
            :class="f.running ? 'bg-green-400' : 'bg-slate-600'"
            :title="f.running ? 'running' : 'stopped'"
          />
          <form v-if="renaming === f.id" class="flex-1 min-w-0" @submit.prevent="saveRename(f.id)">
            <input
              v-model="newName"
              class="w-full bg-slate-800 border border-slate-600 rounded px-2 py-1 text-sm text-slate-100"
              aria-label="Floor name"
              autofocus
              @keydown.escape="renaming = ''"
            />
          </form>
          <a v-else :href="hrefFor(f.id)" class="flex-1 min-w-0">
            <div class="text-sm text-slate-200 truncate">
              {{ f.name }}
              <span v-if="f.template" class="text-xs text-slate-500">· {{ f.template }}</span>
            </div>
            <div v-if="f.description" class="text-xs text-slate-500 truncate">{{ f.description }}</div>
          </a>
          <template v-if="manageable">
            <button
              v-if="renaming === f.id"
              class="text-xs text-slate-300 hover:text-slate-100 px-1"
              @click="saveRename(f.id)"
            >Save</button>
            <button
              v-else
              :disabled="busy !== ''"
              class="text-xs text-slate-500 hover:text-slate-300 disabled:opacity-50 px-1"
              @click="startRename(f)"
            >Rename</button>
            <a :href="editHrefFor(f.id)" class="text-xs text-slate-500 hover:text-slate-300 px-1">Edit</a>
            <button
              v-if="f.running"
              :disabled="busy !== ''"
              class="text-xs text-slate-500 hover:text-slate-300 disabled:opacity-50 px-1"
              @click="stopFloor(f.id)"
            >{{ busy === f.id ? 'Stopping…' : 'Stop' }}</button>
            <button
              :disabled="busy !== ''"
              class="text-xs px-1 disabled:opacity-50"
              :class="confirmDelete === f.id ? 'text-red-400 hover:text-red-300' : 'text-slate-500 hover:text-red-400'"
              :title="confirmDelete === f.id ? 'Deletes the floor\'s directory; its sessions stay' : ''"
              @click="deleteFloor(f.id)"
            >{{ confirmDelete === f.id ? 'Really delete?' : 'Delete' }}</button>
          </template>
        </li>
      </ul>
    </div>
  </div>
</template>
