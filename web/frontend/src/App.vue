<script setup lang="ts">
import { computed } from 'vue'

import AppShell from './components/AppShell.vue'
import { boot } from './boot'
import InboundsView from './views/InboundsView.vue'
import LoginView from './views/LoginView.vue'
import SettingView from './views/SettingView.vue'
import StatusView from './views/StatusView.vue'

const page = computed(() => boot.page)
</script>

<template>
  <LoginView v-if="page === 'login'" />
  <AppShell v-else>
    <StatusView v-if="page === 'status'" />
    <InboundsView v-else-if="page === 'inbounds'" />
    <SettingView v-else-if="page === 'setting'" />
  </AppShell>
</template>
