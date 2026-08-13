import { createApp } from 'vue'
import App from './App.vue'
import { ensureContext } from './api/client'
import { router } from './router'
import './styles/base.css'

void ensureContext()
createApp(App).use(router).mount('#app')
