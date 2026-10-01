<script setup>
defineProps({
  sessions: { type: Array, default: () => [] },
  // hrefFor(id) is the URL that opens session id; hrefFor(null) starts a new one.
  hrefFor: { type: Function, required: true },
})

function when(s) {
  const t = s.last_activity || s.created_at
  return t ? new Date(t).toLocaleString() : ''
}
</script>

<template>
  <div class="flex-1 overflow-y-auto px-4 py-6">
    <div class="max-w-2xl mx-auto">
      <div class="flex items-center justify-between mb-4">
        <h2 class="text-lg font-semibold text-slate-200">Sessions</h2>
        <a
          :href="hrefFor(null)"
          class="px-3 py-1.5 rounded bg-slate-700 hover:bg-slate-600 text-sm text-slate-100 transition-colors"
        >New session</a>
      </div>
      <p v-if="sessions.length === 0" class="text-slate-500 text-sm">No sessions yet.</p>
      <ul v-else class="divide-y divide-slate-800 border border-slate-800 rounded">
        <li v-for="s in sessions" :key="s.id">
          <a
            :href="hrefFor(s.id)"
            class="flex items-center justify-between px-4 py-3 hover:bg-slate-800/60 transition-colors"
          >
            <span class="font-mono text-sm text-slate-300">{{ s.id.slice(0, 8) }}</span>
            <span class="text-xs text-slate-500">
              {{ s.event_count }} {{ s.event_count === 1 ? 'message' : 'messages' }} · {{ when(s) }}
            </span>
          </a>
        </li>
      </ul>
    </div>
  </div>
</template>
