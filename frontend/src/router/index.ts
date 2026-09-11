import { createRouter, createWebHashHistory } from 'vue-router'
import FoldersView from '../views/FoldersView.vue'
import TasksView from '../views/TasksView.vue'
import ReconcileView from '../views/ReconcileView.vue'
import SettingsView from '../views/SettingsView.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/folders' },
    { path: '/folders', component: FoldersView },
    { path: '/tasks', component: TasksView },
    { path: '/reconcile', component: ReconcileView },
    { path: '/settings', component: SettingsView },
  ],
})

export default router
