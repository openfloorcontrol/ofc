<script setup>
import { ref } from 'vue'

defineProps({
  toolCall: { type: Object, required: true },
})

const expanded = ref(false)
</script>

<template>
  <div class="border border-slate-700 rounded text-xs">
    <button
      class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-300 hover:bg-slate-800/50 transition-colors"
      @click="expanded = !expanded"
    >
      <span v-if="toolCall.loading" class="text-yellow-400 animate-spin">&#9696;</span>
      <span v-else class="text-green-400">&#10003;</span>
      <span class="font-mono truncate">{{ toolCall.title }}</span>
      <span class="ml-auto text-slate-500">{{ expanded ? '&#9650;' : '&#9660;' }}</span>
    </button>
    <div v-if="expanded && toolCall.output" class="px-3 py-2 border-t border-slate-700 bg-slate-800/30">
      <pre class="whitespace-pre-wrap text-slate-400 font-mono text-xs leading-relaxed">{{ toolCall.output }}</pre>
    </div>
  </div>
</template>
