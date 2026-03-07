<script setup>
defineProps({
  floorName: { type: String, default: 'OFC' },
  description: { type: String, default: '' },
  agents: { type: Array, default: () => [] },
})

const agentColors = [
  'bg-green-500/20 text-green-400',
  'bg-purple-500/20 text-purple-400',
  'bg-yellow-500/20 text-yellow-400',
  'bg-blue-500/20 text-blue-400',
  'bg-red-500/20 text-red-400',
]

function colorForAgent(index) {
  return agentColors[index % agentColors.length]
}
</script>

<template>
  <header class="border-b border-slate-700 px-4 py-3 flex-shrink-0">
    <div class="flex items-center justify-between">
      <div class="flex items-center gap-3">
        <span class="text-xl font-bold tracking-tight text-slate-100">ofc<span class="ml-1">🎤</span></span>
        <span class="text-slate-600">|</span>
        <div>
          <h1 class="text-sm font-semibold text-slate-300">{{ floorName }}</h1>
          <p v-if="description" class="text-xs text-slate-500">{{ description }}</p>
        </div>
      </div>
      <div class="flex gap-2">
        <span
          v-for="(agent, i) in agents"
          :key="agent.id"
          class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
          :class="colorForAgent(i)"
        >
          {{ agent.id }}
          <span class="ml-1 opacity-60">{{ agent.type }}</span>
        </span>
      </div>
    </div>
  </header>
</template>
