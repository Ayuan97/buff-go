import { onMounted, onUnmounted } from 'vue'

/** 顶层弹层用 Esc 关闭；后注册的后处理（后开的先关）。 */
export function useEscape(handler: () => void) {
  onMounted(() => window.addEventListener('keydown', onKey))
  onUnmounted(() => window.removeEventListener('keydown', onKey))

  function onKey(e: KeyboardEvent) {
    if (e.key !== 'Escape') return
    handler()
  }
}
