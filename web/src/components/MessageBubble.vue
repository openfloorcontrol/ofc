<script setup>
import { computed } from 'vue'
import { useMarkdown } from '../composables/useMarkdown.js'
import ToolCall from './ToolCall.vue'

const props = defineProps({
  message: { type: Object, required: true },
  agents: { type: Array, default: () => [] },
})

const { renderMarkdown } = useMarkdown()

const senderColors = {
  '@user': 'text-cyan-400',
}

const agentColorList = [
  'text-green-400',
  'text-purple-400',
  'text-yellow-400',
  'text-blue-400',
  'text-red-400',
]

const senderColor = computed(() => {
  if (senderColors[props.message.from]) return senderColors[props.message.from]
  const idx = props.agents.findIndex((a) => a.id === props.message.from)
  if (idx >= 0) return agentColorList[idx % agentColorList.length]
  return 'text-slate-400'
})

const isUser = computed(() => props.message.from === '@user')

// Build ordered segments from streaming data or fall back to toolInteractions + content
const segments = computed(() => {
  // If we have streaming segments (interleaved text + tool calls), use those
  if (props.message.segments && props.message.segments.length > 0) {
    return props.message.segments
  }

  // Fall back: toolInteractions from the final message (no ordering info)
  const result = []
  if (props.message.toolInteractions && props.message.toolInteractions.length > 0) {
    for (const ti of props.message.toolInteractions) {
      result.push({
        type: 'tool',
        title: ti.command || ti.Command,
        output: ti.output || ti.Output,
        loading: false,
      })
    }
  }
  if (props.message.content) {
    result.push({ type: 'text', content: props.message.content })
  }
  return result
})

</script>

<template>
  <div class="group">
    <div class="flex items-baseline gap-2 mb-1">
      <span class="text-sm font-semibold" :class="senderColor">{{ message.from }}</span>
      <span v-if="message.isPass" class="text-xs text-slate-500">[PASS]</span>
      <span v-if="message.isError" class="text-xs text-red-400">[ERROR]</span>
    </div>

    <template v-for="(seg, i) in segments" :key="i">
      <div
        v-if="seg.type === 'text' && seg.content && !message.isPass"
        class="prose prose-invert prose-sm max-w-none text-slate-200 mb-2"
        v-html="renderMarkdown(seg.content)"
      />
      <div v-else-if="seg.type === 'tool'" class="mb-2">
        <ToolCall :toolCall="seg" />
      </div>
    </template>
  </div>
</template>
