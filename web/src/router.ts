import { createRouter, createWebHistory } from 'vue-router'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/overview' },
    { path: '/overview', component: () => import('./pages/OverviewPage.vue') },
    { path: '/collection', redirect: '/overview' },
    { path: '/resources', component: () => import('./pages/ResourcesPage.vue') },
    { path: '/config', component: () => import('./pages/ConfigPage.vue') },
    { path: '/runs', component: () => import('./pages/RunsPage.vue') },
    { path: '/market', component: () => import('./pages/MarketPage.vue') },
    { path: '/rules', component: () => import('./pages/RulesPage.vue') },
  ],
})
