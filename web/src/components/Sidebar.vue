<script setup>
import TaskBoardPanel from './TaskBoardPanel.vue'

defineProps({
  furniture: { type: Array, default: () => [] },
  callTool: { type: Function, required: true },
  refreshKey: { type: Number, default: 0 },
})

function isTaskBoard(item) {
  const toolNames = item.tools.map((t) => t.name)
  return toolNames.includes('list_tasks') && toolNames.includes('add_task')
}
</script>

<template>
  <aside class="w-80 flex-shrink-0 border-r border-slate-700 overflow-y-auto bg-slate-850">
    <div v-for="item in furniture" :key="item.name">
      <TaskBoardPanel
        v-if="isTaskBoard(item)"
        :furnitureName="item.name"
        :callTool="callTool"
        :refreshKey="refreshKey"
      />
      <!-- Future: GenericFurniturePanel for unknown furniture types -->
      <div v-else class="p-3">
        <h3 class="text-sm font-semibold text-slate-300 uppercase tracking-wide">
          {{ item.name }}
        </h3>
        <p class="text-xs text-slate-500 mt-1">
          {{ item.tools.length }} tool{{ item.tools.length !== 1 ? 's' : '' }}
        </p>
      </div>
    </div>
  </aside>
</template>
