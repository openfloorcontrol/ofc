<script setup>
import { computed } from 'vue'
import { useMarkdown } from '../composables/useMarkdown.js'
import ToolCall from './ToolCall.vue'

const props = defineProps({
  message: { type: Object, required: true },
  agents: { type: Array, default: () => [] },
})

const { renderMarkdown } = useMarkdown()

const agentColorList = [
  'text-green-400',
  'text-purple-400',
  'text-yellow-400',
  'text-blue-400',
  'text-red-400',
]

const senderColor = computed(() => {
  const idx = props.agents.findIndex((a) => a.id === props.message.from)
  if (idx >= 0) return agentColorList[idx % agentColorList.length]
  return 'text-slate-400'
})

</script>

<template>
  <div class="group">
    <div class="flex items-baseline gap-2 mb-1">
      <span class="text-sm font-semibold" :class="senderColor">{{ message.from }}</span>
      <span class="inline-block w-2 h-2 bg-slate-400 animate-pulse rounded-full" />
    </div>

    <template v-for="(seg, i) in message.segments" :key="i">
      <div
        v-if="seg.type === 'text' && seg.content"
        class="prose prose-invert prose-sm max-w-none text-slate-200 mb-2"
        v-html="renderMarkdown(seg.content)"
      />
      <div v-else-if="seg.type === 'tool'" class="mb-2">
        <ToolCall :toolCall="seg" />
      </div>
    </template>
  </div>
</template>
