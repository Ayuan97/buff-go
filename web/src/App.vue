<script setup lang="ts">
const nav = [
  { to: '/overview', label: '概览' },
  { to: '/resources', label: '资源' },
  { to: '/config', label: '配置' },
  { to: '/runs', label: '运行' },
  { to: '/market', label: '行情' },
  { to: '/rules', label: '规则' },
]
</script>

<template>
  <div class="shell">
    <aside class="rail">
      <div class="rail-brand">
        <div class="sig">BG</div>
        <div class="sig-meta">
          <div class="sig-name">BUFF-GO</div>
          <div class="sig-sub">本机控制台</div>
        </div>
      </div>
      <nav class="rail-nav">
        <RouterLink v-for="n in nav" :key="n.to" :to="n.to" class="rail-link">
          <span class="idx">{{ String(nav.indexOf(n) + 1).padStart(2, '0') }}</span>
          {{ n.label }}
        </RouterLink>
      </nav>
      <div class="rail-foot">
        <span class="tag">LOCAL</span>
      </div>
    </aside>
    <div class="stage">
      <RouterView />
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: minmax(0, 200px) minmax(0, 1fr);
  min-height: 100vh;
  width: 100%;
  background: var(--bg);
}
.rail {
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--line);
  background: var(--bg-elev);
  position: sticky;
  top: 0;
  height: 100vh;
}
.rail-brand {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 18px 14px;
  border-bottom: 1px solid var(--line);
}
.sig {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
  background: var(--accent);
  color: #fff;
  font-family: var(--mono);
  font-weight: 800;
  font-size: 12px;
  letter-spacing: -0.04em;
}
.sig-name {
  font-weight: 800;
  font-size: 12px;
  letter-spacing: 0.14em;
  font-family: var(--mono);
}
.sig-sub {
  margin-top: 2px;
  font-family: var(--mono);
  font-size: 9px;
  letter-spacing: 0.16em;
  color: var(--text-3);
}
.rail-nav {
  display: flex;
  flex-direction: column;
  padding: 10px 0;
  flex: 1;
}
.rail-link {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 14px;
  color: var(--text-2);
  border-bottom: none;
  border-left: 2px solid transparent;
  font-family: var(--mono);
  font-size: 12px;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  text-decoration: none;
}
.rail-link:hover {
  color: var(--text);
  background: var(--surface);
  border-bottom: none;
}
.rail-link.router-link-active {
  color: #fff;
  background: var(--surface);
  border-left-color: var(--accent);
  font-weight: 700;
}
.idx {
  color: var(--text-3);
  font-size: 10px;
  min-width: 18px;
}
.rail-link.router-link-active .idx { color: var(--accent); }
.rail-foot {
  padding: 14px;
  border-top: 1px solid var(--line);
  display: flex;
  align-items: center;
  gap: 10px;
}
.tag {
  font-family: var(--mono);
  font-size: 9px;
  font-weight: 800;
  letter-spacing: 0.14em;
  padding: 3px 6px;
  background: var(--warn-dim);
  color: var(--warn);
  border: 1px solid color-mix(in srgb, var(--warn) 40%, transparent);
}
.rail-note {
  font-family: var(--mono);
  font-size: 9px;
  letter-spacing: 0.12em;
  color: var(--text-3);
}
.stage {
  min-width: 0;
  width: 100%;
  min-height: 100vh;
  overflow-x: hidden;
}
@media (max-width: 900px) {
  .shell { grid-template-columns: 1fr; }
  .rail {
    position: sticky;
    height: auto;
    flex-direction: row;
    flex-wrap: wrap;
    align-items: center;
    border-right: none;
    border-bottom: 1px solid var(--line);
  }
  .rail-brand { border-bottom: none; padding: 10px 12px; }
  .rail-nav {
    flex-direction: row;
    flex: 1;
    overflow-x: auto;
    padding: 0;
  }
  .rail-link {
    padding: 12px 10px;
    border-left: none;
    border-bottom: 2px solid transparent;
    white-space: nowrap;
  }
  .rail-link.router-link-active {
    border-left: none;
    border-bottom-color: var(--accent);
  }
  .idx { display: none; }
  .rail-foot { border-top: none; margin-left: auto; }
}
</style>
