<script setup lang="ts">
import { computed, ref } from 'vue'
import type { SteamFacetsVocab, Target } from '../api'
import { useEscape } from '../utils/escape'
import { gameName, sideText } from '../utils/format'

const props = defineProps<{
  target: Target
  vocab: SteamFacetsVocab
  busy: boolean
}>()

const emit = defineEmits<{
  save: [cats: string[], classes: string[]]
  cancel: []
}>()

const query = ref('')
const cats = ref([...(props.target.steam_cats ?? [])])
const classes = ref([...(props.target.item_classes ?? [])])

useEscape(() => emit('cancel'))

const groups = computed(() =>
  props.vocab.categories.map((category) => ({
    ...category,
    items: props.vocab.item_classes.filter((item) => item.category === category.slug),
  })),
)

const filteredGroups = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return groups.value
    .map((group) => ({
      ...group,
      items: needle
        ? group.items.filter((item) =>
            `${item.name} ${item.label}`.toLowerCase().includes(needle),
          )
        : group.items,
    }))
    .filter((group) => group.items.length > 0)
})

const dirty = computed(() => {
  const same = (left: string[], right: string[]) =>
    [...left].sort().join(',') === [...right].sort().join(',')
  return !same(cats.value, props.target.steam_cats ?? []) || !same(classes.value, props.target.item_classes ?? [])
})

function toggleCat(slug: string) {
  cats.value = cats.value.includes(slug)
    ? cats.value.filter((item) => item !== slug)
    : [...cats.value, slug]
}

function toggleClass(slug: string) {
  classes.value = classes.value.includes(slug)
    ? classes.value.filter((item) => item !== slug)
    : [...classes.value, slug]
}

function groupSelected(slugs: string[]): boolean {
  return slugs.length > 0 && slugs.every((slug) => classes.value.includes(slug))
}

function toggleGroup(slugs: string[]) {
  const allOn = groupSelected(slugs)
  if (allOn) {
    classes.value = classes.value.filter((slug) => !slugs.includes(slug))
    return
  }
  classes.value = [...new Set([...classes.value, ...slugs])]
}

function clearAll() {
  cats.value = []
  classes.value = []
}

function save() {
  if (!dirty.value) {
    emit('cancel')
    return
  }
  emit('save', cats.value, classes.value)
}
</script>

<template>
  <div class="modal-mask" @click.self="emit('cancel')">
    <form class="modal wide" @submit.prevent="save">
      <div class="modal-head">
        Steam 筛选 · {{ gameName(target.appid) }} · {{ sideText(target.side) }}
      </div>
      <div class="modal-body">
        <p class="note">
          勾选内容会作为 Steam 搜索参数提交；页面显示中文，实际发送英文参数。
        </p>
        <p v-if="target.side === 'bid'" class="note warn">求购没有这些搜索参数，存了也不会打出去。</p>
        <p class="note warn">保存后该方向未完成任务会丢掉，补货从头开始。已经存下来的行情不会丢。</p>

        <div class="field">
          <label>分类</label>
          <div class="chips">
            <label v-for="cat in vocab.categories" :key="cat.slug" class="chip" :class="{ on: cats.includes(cat.slug) }">
              <input type="checkbox" :checked="cats.includes(cat.slug)" @change="toggleCat(cat.slug)" />
              {{ cat.name }}
            </label>
          </div>
        </div>

        <div class="field">
          <label>物品类型</label>
          <input v-model="query" class="search" placeholder="按中文或英文筛选" />
        </div>

        <div class="groups">
          <section v-for="group in filteredGroups" :key="group.slug" class="group">
            <div class="group-head">
              <span>{{ group.name }}</span>
              <button
                v-if="group.items.length"
                class="link"
                type="button"
                @click="toggleGroup(group.items.map((item) => item.slug))"
              >
                {{ groupSelected(group.items.map((item) => item.slug)) ? '取消本组' : '全选本组' }}
              </button>
            </div>
            <div class="items">
              <label v-for="item in group.items" :key="item.slug" class="check">
                <input
                  type="checkbox"
                  :checked="classes.includes(item.slug)"
                  @change="toggleClass(item.slug)"
                />
                <span>{{ item.name }}</span>
                <span class="en">{{ item.label }}</span>
              </label>
            </div>
          </section>
        </div>
      </div>
      <div class="modal-foot">
        <button class="btn" type="button" @click="clearAll">清空</button>
        <span class="spacer" />
        <button class="btn" type="button" @click="emit('cancel')">取消</button>
        <button class="btn primary" type="submit" :disabled="busy || !dirty">保存</button>
      </div>
    </form>
  </div>
</template>

<style scoped>
.wide { width: 720px; }
.note { margin: 0 0 8px; color: var(--text-3); font-size: 12px; }
.note.warn { color: var(--warn); }
.field { margin: 12px 0 0; }
.field > label {
  display: block;
  margin-bottom: 6px;
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 11px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.chips { display: flex; flex-wrap: wrap; gap: 8px; }
.chip {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 10px;
  border: 1px solid var(--line-strong);
  font-size: 12px;
}
.chip.on { background: var(--bg-elev); }
.chip input { margin: 0; }
.search {
  width: 100%;
  height: 28px;
  padding: 0 8px;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--line-strong);
  font-size: 12px;
}
.groups { margin-top: 12px; max-height: 360px; overflow: auto; }
.group { margin-bottom: 14px; }
.group-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  color: var(--text-2);
  font-size: 12px;
  font-weight: 600;
}
.link {
  border: 0;
  background: none;
  color: var(--text-3);
  cursor: pointer;
  font-size: 11px;
}
.items {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 4px 12px;
}
.check { display: flex; align-items: center; gap: 6px; font-size: 12px; }
.en { color: var(--text-3); font-size: 11px; }
.spacer { flex: 1; }
</style>
