<script setup>
import { ref, computed, onMounted, nextTick } from 'vue'
import { apiFetch } from '../composables/useAuth.js'
import { apiError } from '../composables/useFloor.js'

const props = defineProps({
  floorId: { type: String, required: true },
  // floorHref is the URL of the floor's conversation.
  floorHref: { type: String, required: true },
})

const url = `/api/v1/floors/${encodeURIComponent(props.floorId)}/blueprint`
const text = ref('')
const saved = ref('') // the text as last loaded or saved
const error = ref('')
const status = ref('') // 'saving', 'saved'
const textarea = ref(null)
const gutter = ref(null)

const lineCount = computed(() => text.value.split('\n').length)
const dirty = computed(() => text.value !== saved.value)

onMounted(async () => {
  try {
    const resp = await apiFetch(url)
    if (!resp.ok) throw await apiError(resp)
    text.value = (await resp.json()).blueprint
    saved.value = text.value
  } catch (err) {
    error.value = err.message
  }
})

async function save() {
  error.value = ''
  status.value = 'saving'
  try {
    const resp = await apiFetch(url, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ blueprint: text.value }),
    })
    if (!resp.ok) throw await apiError(resp)
    saved.value = text.value
    status.value = 'saved'
  } catch (err) {
    status.value = ''
    error.value = err.message
    goToLine(err.message)
  }
}

// goToLine selects the first line an error message names ("line 4: …").
async function goToLine(message) {
  const m = /line (\d+)/.exec(message)
  if (!m || !textarea.value) return
  const lines = text.value.split('\n')
  const n = Math.min(Number(m[1]), lines.length) - 1
  const start = lines.slice(0, n).reduce((sum, l) => sum + l.length + 1, 0)
  await nextTick()
  textarea.value.focus()
  textarea.value.setSelectionRange(start, start + lines[n].length)
  const lineHeight = textarea.value.scrollHeight / lines.length
  textarea.value.scrollTop = Math.max(0, (n - 3) * lineHeight)
}

function syncGutter() {
  gutter.value.scrollTop = textarea.value.scrollTop
}

// Ctrl/Cmd+S saves; Tab inserts two spaces instead of leaving the editor.
function onKeydown(e) {
  if ((e.ctrlKey || e.metaKey) && e.key === 's') {
    e.preventDefault()
    save()
  } else if (e.key === 'Tab' && !e.shiftKey) {
    e.preventDefault()
    const t = textarea.value
    const at = t.selectionStart
    text.value = text.value.slice(0, at) + '  ' + text.value.slice(t.selectionEnd)
    nextTick(() => t.setSelectionRange(at + 2, at + 2))
  }
}
</script>

<template>
  <div class="flex flex-col flex-1 min-h-0 px-4 py-4">
    <div class="flex items-center justify-between gap-3 mb-3">
      <h2 class="text-sm font-semibold text-slate-300">
        blueprint.yaml
        <span v-if="dirty" class="text-slate-500 font-normal">· unsaved</span>
      </h2>
      <div class="flex items-center gap-3">
        <span v-if="status === 'saved' && !dirty" class="text-xs text-green-400">
          Saved — the floor starts with it on its next request.
        </span>
        <a :href="floorHref" class="text-xs text-slate-500 hover:text-slate-300">Back to the floor</a>
        <button
          :disabled="!dirty || status === 'saving'"
          class="px-3 py-1.5 rounded bg-slate-700 hover:bg-slate-600 disabled:opacity-50 text-sm text-slate-100 transition-colors"
          title="Save (Ctrl+S)"
          @click="save"
        >{{ status === 'saving' ? 'Checking…' : 'Save' }}</button>
      </div>
    </div>
    <div
      v-if="error"
      class="mb-3 rounded border border-red-500/40 bg-red-500/10 px-3 py-2 text-sm text-red-300 font-mono whitespace-pre-wrap"
    >{{ error }}</div>
    <div class="flex flex-1 min-h-0 border border-slate-700 rounded overflow-hidden bg-slate-950">
      <div
        ref="gutter"
        class="select-none overflow-hidden text-right text-slate-600 font-mono text-sm leading-6 py-2 pl-2 pr-3 border-r border-slate-800"
        aria-hidden="true"
      >
        <div v-for="n in lineCount" :key="n">{{ n }}</div>
      </div>
      <textarea
        ref="textarea"
        v-model="text"
        spellcheck="false"
        aria-label="Blueprint"
        class="flex-1 resize-none bg-transparent text-slate-200 font-mono text-sm leading-6 py-2 px-3 outline-none whitespace-pre overflow-auto"
        @scroll="syncGutter"
        @keydown="onKeydown"
      />
    </div>
  </div>
</template>
